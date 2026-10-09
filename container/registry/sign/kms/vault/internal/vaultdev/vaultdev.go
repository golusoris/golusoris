// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package vaultdev boots a HashiCorp Vault or OpenBao dev server via
// testcontainers-go, with a random per-test root token, and calls its HTTP
// API for test setup. Docker is required; -short or an unhealthy Docker
// provider skips the test. It lives in this module, not in root testutil,
// so the module's tests need no root golusoris release.
//
//	srv := vaultdev.Start(t, vaultdev.OpenBao)
//	srv.Write(t, "sys/mounts/transit", map[string]any{"type": "transit"})
package vaultdev

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Flavor selects the server image.
type Flavor string

// Server flavors. Both serve the same transit and auth HTTP API.
const (
	Vault   Flavor = "vault"
	OpenBao Flavor = "openbao"
)

const (
	apiPort = "8200/tcp"
	// startTimeout bounds one container start including a cold image pull,
	// as in testutil/pg.
	startTimeout = 3 * time.Minute
	stopTimeout  = 10 * time.Second
	callTimeout  = 30 * time.Second
	readyLog     = "Development mode should NOT be used in production installations!"
	// portTries bounds the search for two adjacent free host ports.
	portTries = 32
	maxBody   = 1 << 20
)

// Server is a running dev-mode server: unsealed, in-memory, root token set.
type Server struct {
	// Address is the API base URL without the /v1 prefix.
	Address string
	// Token is the root token.
	Token  string
	Flavor Flavor
}

// Start boots a dev server on a mapped port.
func Start(t *testing.T, f Flavor) Server {
	t.Helper()
	image, prefix := flavor(t, f)
	token := rootToken(t)
	addr := start(t, testcontainers.ContainerRequest{
		Image:        image,
		ExposedPorts: []string{apiPort},
		Env:          map[string]string{prefix + "_DEV_ROOT_TOKEN_ID": token},
		WaitingFor:   wait.ForLog(readyLog),
	}, true)
	return ready(t, Server{Address: "http://" + addr, Token: token, Flavor: f})
}

// StartHostNetwork boots a dev server on the host network, listening on
// 127.0.0.1, so the server reaches httptest servers of the test (such as a
// Kubernetes TokenReview endpoint). Docker Desktop has no host networking:
// the test is skipped outside Linux.
func StartHostNetwork(t *testing.T, f Flavor) Server {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("testutil/vaultdev: host networking needs a Linux Docker engine, not %s", runtime.GOOS)
	}
	image, prefix := flavor(t, f)
	token := rootToken(t)
	listen := "127.0.0.1:" + strconv.Itoa(freePortPair(t))
	start(t, testcontainers.ContainerRequest{
		Image: image,
		Env: map[string]string{
			prefix + "_DEV_ROOT_TOKEN_ID":  token,
			prefix + "_DEV_LISTEN_ADDRESS": listen,
		},
		HostConfigModifier: func(hc *container.HostConfig) { hc.NetworkMode = "host" },
		WaitingFor:         wait.ForLog(readyLog),
	}, false)
	return ready(t, Server{Address: "http://" + listen, Token: token, Flavor: f})
}

// Response is the API envelope; Data and Auth are nil when absent.
type Response struct {
	Data map[string]any `json:"data"`
	Auth map[string]any `json:"auth"`
}

// Write sends body to path (without /v1) with the root token. A non-2xx
// answer fails the test.
func (s Server) Write(t *testing.T, path string, body any) Response {
	t.Helper()
	return s.call(t, http.MethodPost, path, body)
}

// Read reads path (without /v1) with the root token. A non-2xx answer fails
// the test.
func (s Server) Read(t *testing.T, path string) Response {
	t.Helper()
	return s.call(t, http.MethodGet, path, nil)
}

func (s Server) call(t *testing.T, method, path string, body any) Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), callTimeout)
	defer cancel()
	status, raw, err := do(ctx, method, s.Address+"/v1/"+path, s.Token, body)
	if err != nil {
		t.Fatalf("testutil/vaultdev: %s %s: %v", method, path, err)
	}
	if status/100 != 2 {
		t.Fatalf("testutil/vaultdev: %s %s = %d %s", method, path, status, bytes.TrimSpace(raw))
	}
	var out Response
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("testutil/vaultdev: %s %s: decode: %v", method, path, err)
		}
	}
	return out
}

func do(ctx context.Context, method, target, token string, body any) (status int, raw []byte, err error) {
	var rd io.Reader
	if body != nil {
		b, merr := json.Marshal(body)
		if merr != nil {
			return 0, nil, fmt.Errorf("encode: %w", merr)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("X-Vault-Token", token)
	client := &http.Client{Timeout: callTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()
	raw, err = io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return resp.StatusCode, raw, err
}

func flavor(t *testing.T, f Flavor) (image, envPrefix string) {
	t.Helper()
	switch f {
	case Vault:
		return VaultImage, "VAULT"
	case OpenBao:
		return OpenBaoImage, "BAO"
	default:
		t.Fatalf("testutil/vaultdev: unknown flavor %q", f)
		return "", ""
	}
}

func rootToken(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("testutil/vaultdev: root token: %v", err)
	}
	return "root-" + hex.EncodeToString(b)
}

// freePortPair returns a free host port p with p+1 also free: dev mode puts
// the cluster listener on the next port.
func freePortPair(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	for range portTries {
		l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("testutil/vaultdev: free port: %v", err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		next, nerr := lc.Listen(t.Context(), "tcp", "127.0.0.1:"+strconv.Itoa(port+1))
		closeListener(t, l)
		if nerr == nil {
			closeListener(t, next)
			return port
		}
	}
	t.Fatalf("testutil/vaultdev: no two adjacent free ports in %d tries", portTries)
	return 0
}

func closeListener(t *testing.T, l net.Listener) {
	t.Helper()
	if err := l.Close(); err != nil {
		t.Fatalf("testutil/vaultdev: close probe listener: %v", err)
	}
}

// start boots req and returns host:port of apiPort when mapped is set.
func start(t *testing.T, req testcontainers.ContainerRequest, mapped bool) string {
	t.Helper()
	if testing.Short() {
		t.Skip("testutil/vaultdev: container-backed; skipped under -short")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	req.ReaperImage = RyukImage
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("testutil/vaultdev: start container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), stopTimeout)
		defer stopCancel()
		if termErr := ctr.Terminate(stopCtx); termErr != nil {
			t.Logf("testutil/vaultdev: terminate container: %v", termErr)
		}
	})
	if !mapped {
		return ""
	}
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("testutil/vaultdev: host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, apiPort)
	if err != nil {
		t.Fatalf("testutil/vaultdev: mapped port: %v", err)
	}
	return net.JoinHostPort(host, port.Port())
}

// ready polls sys/health until the server answers unsealed and active.
func ready(t *testing.T, s Server) Server {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), callTimeout)
	defer cancel()
	var last error
	for range 60 {
		status, _, err := do(ctx, http.MethodGet, s.Address+"/v1/sys/health", "", nil)
		if err == nil && status == http.StatusOK {
			return s
		}
		last = errors.Join(err, fmt.Errorf("status %d", status))
		select {
		case <-ctx.Done():
			t.Fatalf("testutil/vaultdev: not ready: %v", errors.Join(last, ctx.Err()))
		case <-time.After(250 * time.Millisecond):
		}
	}
	t.Fatalf("testutil/vaultdev: not ready: %v", last)
	return s
}
