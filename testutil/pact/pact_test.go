// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pact_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pact-foundation/pact-go/v2/consumer"

	"github.com/golusoris/golusoris/testutil/pact"
)

const (
	consumerName = "GolusorisConsumer"
	providerName = "GolusorisProvider"
	// childEnv selects one failing scenario when the test binary re-runs itself.
	childEnv = "GOLUSORIS_PACT_CHILD"
)

// addUserInteraction expects GET /users/1 answered with 200 and body "ada".
func addUserInteraction(p *pact.HTTPPact) {
	p.AddInteraction().
		UponReceiving("a GET /users/1 request").
		WithRequest(http.MethodGet, "/users/1").
		WillRespondWith(http.StatusOK, func(b *consumer.V2ResponseBuilder) {
			b.Body("text/plain", []byte("ada"))
		})
}

// recordPact runs the consumer side against the mock server and returns the
// pact file it wrote below the working directory.
func recordPact(t *testing.T) string {
	t.Helper()
	p := pact.NewHTTPPact(t, consumerName, providerName)
	addUserInteraction(p)
	p.ExecuteTest(t, func(cfg consumer.MockServerConfig) error {
		return getUser(t, fmt.Sprintf("http://%s:%d", cfg.Host, cfg.Port))
	})
	return filepath.Join("pacts", consumerName+"-"+providerName+".json")
}

func getUser(t *testing.T, base string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/users/1", http.NoBody)
	if err != nil {
		return fmt.Errorf("pact test: request: %w", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return fmt.Errorf("pact test: get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err = io.Copy(io.Discard, resp.Body); err != nil {
		return fmt.Errorf("pact test: read: %w", err)
	}
	return nil
}

// startProvider serves /users/1 with the given status.
func startProvider(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/1" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "ada")
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func verify(t *testing.T, base, file string) {
	t.Helper()
	pact.VerifyProvider(t, pact.ProviderOptions{
		Provider:        providerName,
		ProviderBaseURL: base,
		PactURLs:        []string{file},
	})
}

// runChild re-runs this test binary for one failing scenario and returns its
// combined output; the child must exit non-zero.
func runChild(t *testing.T, scenario string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	//nolint:gosec // G204: re-executes this test binary with a fixed -test.run pattern.
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), childEnv+"="+scenario)
	out, err := cmd.CombinedOutput()
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		t.Fatalf("child %s: want non-zero exit, got %v\n%s", scenario, err, out)
	}
	return string(out)
}

// TestChild hosts the failing scenarios; it does nothing unless runChild set
// the scenario.
//
//nolint:paralleltest // child scenarios call t.Chdir.
func TestChild(t *testing.T) {
	switch os.Getenv(childEnv) {
	case "":
		t.Skip("runs only as a child of the negative tests")
	case "broken-provider":
		t.Chdir(t.TempDir())
		verify(t, startProvider(t, http.StatusInternalServerError), recordPact(t))
	case "unmet-interaction":
		t.Chdir(t.TempDir())
		p := pact.NewHTTPPact(t, consumerName, providerName)
		addUserInteraction(p)
		p.ExecuteTest(t, func(consumer.MockServerConfig) error { return nil })
	default:
		t.Fatalf("unknown child scenario %q", os.Getenv(childEnv))
	}
}

// TestContractRoundTrip records a pact and verifies a matching provider.
// Not parallel: t.Chdir, and pact-go writes pacts and logs below the
// working directory.
//
//nolint:paralleltest // t.Chdir.
func TestContractRoundTrip(t *testing.T) {
	t.Chdir(t.TempDir())
	file := recordPact(t)
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("pact file not written: %v", err)
	}
	verify(t, startProvider(t, http.StatusOK), file)
}

func TestVerifyProviderFailsOnBrokenProvider(t *testing.T) {
	t.Parallel()
	out := runChild(t, "broken-provider")
	if !strings.Contains(out, "pact: provider verification") || strings.Contains(out, "Failed to load pact") {
		t.Fatalf("child output lacks a contract mismatch:\n%s", out)
	}
}

func TestExecuteTestFailsOnUnmetInteraction(t *testing.T) {
	t.Parallel()
	out := runChild(t, "unmet-interaction")
	if !strings.Contains(out, "pact: execute test") {
		t.Fatalf("child output lacks execute-test failure:\n%s", out)
	}
}
