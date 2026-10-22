// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build integration

package sentry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	sentrygo "github.com/getsentry/sentry-go"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/golusoris/golusoris/internal/testimages"
)

type issueItem struct {
	CalculatedValue    string `json:"calculated_value"`
	DigestedEventCount int    `json:"digested_event_count"`
}

type issuesResponse struct {
	Results []issueItem `json:"results"`
}

type idWrapper struct {
	ID any `json:"id"`
}

type projectDetails struct {
	DSN string `json:"dsn"`
}

type bugSinkClient struct {
	httpClient *http.Client
	baseURL    string
	token      string
	hostPort   string
}

func newBugSinkClient(hostPort, token string) *bugSinkClient {
	return &bugSinkClient{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		baseURL:    "http://" + hostPort + "/api/canonical/0",
		token:      token,
		hostPort:   hostPort,
	}
}

func (c *bugSinkClient) doJSON(ctx context.Context, method, path string, reqBody any, respDst any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		raw, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("observability/sentry: marshal json: %w", err)
		}
		bodyReader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("observability/sentry: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("observability/sentry: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("observability/sentry: http status %d: %s", resp.StatusCode, string(respBytes))
	}

	if respDst != nil {
		if err := json.NewDecoder(resp.Body).Decode(respDst); err != nil {
			return fmt.Errorf("observability/sentry: decode json: %w", err)
		}
	}
	return nil
}

func (c *bugSinkClient) createTeam(ctx context.Context, name string) (string, error) {
	var out idWrapper
	body := map[string]string{"name": name}
	if err := c.doJSON(ctx, http.MethodPost, "/teams/", body, &out); err != nil {
		return "", fmt.Errorf("observability/sentry: create team: %w", err)
	}
	return fmt.Sprintf("%v", out.ID), nil
}

func (c *bugSinkClient) createProject(ctx context.Context, teamID, name string) (string, error) {
	var out idWrapper
	body := map[string]string{"name": name, "team": teamID}
	if err := c.doJSON(ctx, http.MethodPost, "/projects/", body, &out); err != nil {
		return "", fmt.Errorf("observability/sentry: create project: %w", err)
	}
	return fmt.Sprintf("%v", out.ID), nil
}

func (c *bugSinkClient) getProjectDSN(ctx context.Context, projectID string) (string, error) {
	var out projectDetails
	if err := c.doJSON(ctx, http.MethodGet, "/projects/"+projectID+"/", nil, &out); err != nil {
		return "", fmt.Errorf("observability/sentry: get project dsn: %w", err)
	}
	return rewriteDSN(out.DSN, c.hostPort)
}

func (c *bugSinkClient) getIssues(ctx context.Context, projectID string) ([]issueItem, error) {
	var out issuesResponse
	path := "/issues/?project=" + projectID
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, fmt.Errorf("observability/sentry: get issues: %w", err)
	}
	return out.Results, nil
}

func (c *bugSinkClient) pollIssuesUntil(ctx context.Context, projectID string, match func([]issueItem) bool, maxAttempts int) ([]issueItem, error) {
	const pollInterval = 500 * time.Millisecond
	var lastIssues []issueItem
	for range maxAttempts {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("observability/sentry: poll issues context: %w", err)
		}
		issues, err := c.getIssues(ctx, projectID)
		if err == nil {
			lastIssues = issues
			if match(issues) {
				return issues, nil
			}
		}
		select {
		case <-ctx.Done():
			return lastIssues, nil
		case <-time.After(pollInterval):
		}
	}
	return lastIssues, nil
}

func randomHex(t *testing.T, byteLen int) string {
	t.Helper()
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("crypto/rand: %v", err)
	}
	return hex.EncodeToString(b)
}

func rewriteDSN(rawDSN, targetHostPort string) (string, error) {
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		return "", fmt.Errorf("observability/sentry: parse dsn %q: %w", rawDSN, err)
	}
	parsed.Host = targetHostPort
	return parsed.String(), nil
}

func corruptDSNKey(rawDSN string) (string, error) {
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		return "", fmt.Errorf("observability/sentry: parse dsn: %w", err)
	}
	parsed.User = url.User(strings.Repeat("f", 32))
	return parsed.String(), nil
}

func containsCalculatedValue(items []issueItem, substr string) bool {
	for _, item := range items {
		if strings.Contains(item.CalculatedValue, substr) {
			return true
		}
	}
	return false
}

func execBugsinkToken(ctx context.Context, container testcontainers.Container) (string, error) {
	code, reader, err := container.Exec(ctx, []string{"bugsink-manage", "create_auth_token"}, tcexec.Multiplexed())
	if err != nil {
		return "", fmt.Errorf("observability/sentry: exec create_auth_token: %w", err)
	}
	if code != 0 {
		return "", fmt.Errorf("observability/sentry: create_auth_token exit %d", code)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("observability/sentry: read create_auth_token output: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if len(line) == 40 {
			return line, nil
		}
	}
	return "", fmt.Errorf("observability/sentry: 40-character token not found in output: %q", string(out))
}

func startBugsinkContainer(t *testing.T) (testcontainers.Container, string) {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	secretKey := randomHex(t, 32)
	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        testimages.Bugsink,
			ReaperImage:  testimages.Ryuk,
			ExposedPorts: []string{"8000/tcp"},
			Env: map[string]string{
				"SECRET_KEY":       secretKey,
				"CREATE_SUPERUSER": "admin@example.com:admin",
				"PORT":             "8000",
			},
			WaitingFor: wait.ForAll(
				wait.ForListeningPort("8000/tcp"),
				wait.ForHTTP("/").WithPort("8000/tcp").WithStatusCodeMatcher(func(status int) bool {
					return status == http.StatusOK || status == http.StatusFound
				}),
			).WithDeadline(2 * time.Minute),
		},
		Started: true,
	}

	container, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		t.Fatalf("observability/sentry: start bugsink container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer stopCancel()
		if termErr := container.Terminate(stopCtx); termErr != nil {
			t.Logf("observability/sentry: terminate bugsink container: %v", termErr)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("observability/sentry: container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "8000/tcp")
	if err != nil {
		t.Fatalf("observability/sentry: mapped port: %v", err)
	}
	return container, net.JoinHostPort(host, port.Port())
}

type bugSinkHarness struct {
	client    *bugSinkClient
	projectID string
	dsn       string
}

func setupBugsinkHarness(t *testing.T) *bugSinkHarness {
	t.Helper()
	container, hostPort := startBugsinkContainer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	token, err := execBugsinkToken(ctx, container)
	if err != nil {
		t.Fatalf("setupBugsinkHarness token: %v", err)
	}

	client := newBugSinkClient(hostPort, token)
	teamName := "team-" + randomHex(t, 4)
	teamID, err := client.createTeam(ctx, teamName)
	if err != nil {
		t.Fatalf("setupBugsinkHarness team: %v", err)
	}

	projName := "proj-" + randomHex(t, 4)
	projID, err := client.createProject(ctx, teamID, projName)
	if err != nil {
		t.Fatalf("setupBugsinkHarness project: %v", err)
	}

	realDSN, err := client.getProjectDSN(ctx, projID)
	if err != nil {
		t.Fatalf("setupBugsinkHarness dsn: %v", err)
	}

	t.Cleanup(func() {
		_ = Flush(2 * time.Second)
		sentrygo.CurrentHub().BindClient(nil)
	})

	return &bugSinkHarness{
		client:    client,
		projectID: projID,
		dsn:       realDSN,
	}
}

//nolint:paralleltest // WHY: Sentry SDK manages global hub state; parallel execution would corrupt client binding.
func TestIntegration_Positive(t *testing.T) {
	harness := setupBugsinkHarness(t)

	opts := DefaultOptions()
	opts.DSN = harness.dsn
	if err := Init(opts); err != nil {
		t.Fatalf("Init with valid DSN: %v", err)
	}

	slogMsg := "slog-error-" + randomHex(t, 8)
	handler := newSentryHandler()
	logger := slog.New(handler)
	logger.Error(slogMsg)

	if !Flush(5 * time.Second) {
		t.Fatal("Flush returned false after slog error")
	}

	pollCtx, pollCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer pollCancel()

	issues, err := harness.client.pollIssuesUntil(pollCtx, harness.projectID, func(items []issueItem) bool {
		return containsCalculatedValue(items, slogMsg)
	}, 30)
	if err != nil {
		t.Fatalf("poll issues for slog error: %v", err)
	}
	if !containsCalculatedValue(issues, slogMsg) {
		t.Fatalf("expected issue with message %q, got: %+v", slogMsg, issues)
	}

	captureMsg := "sentry-capture-" + randomHex(t, 8)
	eventID := sentrygo.CaptureMessage(captureMsg)
	if eventID == nil {
		t.Fatal("sentrygo.CaptureMessage returned nil eventID")
	}

	if !Flush(5 * time.Second) {
		t.Fatal("Flush returned false after CaptureMessage")
	}

	captureIssues, err := harness.client.pollIssuesUntil(pollCtx, harness.projectID, func(items []issueItem) bool {
		return containsCalculatedValue(items, captureMsg)
	}, 30)
	if err != nil {
		t.Fatalf("poll issues for CaptureMessage: %v", err)
	}
	if !containsCalculatedValue(captureIssues, captureMsg) {
		t.Fatalf("expected issue with message %q, got: %+v", captureMsg, captureIssues)
	}
}

//nolint:paralleltest // WHY: Sentry SDK manages global hub state; parallel execution would corrupt client binding.
func TestIntegration_Negative(t *testing.T) {
	harness := setupBugsinkHarness(t)

	badDSN, err := corruptDSNKey(harness.dsn)
	if err != nil {
		t.Fatalf("corrupt DSN key: %v", err)
	}

	opts := DefaultOptions()
	opts.DSN = badDSN
	if initErr := Init(opts); initErr != nil {
		t.Fatalf("Init with corrupted DSN: %v", initErr)
	}

	handler := newSentryHandler()
	logger := slog.New(handler)
	logger.Error("slog-negative-" + randomHex(t, 8))

	_ = Flush(2 * time.Second)

	pollCtx, pollCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pollCancel()

	issues, err := harness.client.pollIssuesUntil(pollCtx, harness.projectID, func(items []issueItem) bool {
		return len(items) > 0
	}, 6)
	if err != nil {
		t.Fatalf("poll issues: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues with bad DSN key, got %d: %+v", len(issues), issues)
	}
}

//nolint:paralleltest // WHY: Sentry SDK manages global hub state; parallel execution would corrupt client binding.
func TestIntegration_Boundary(t *testing.T) {
	harness := setupBugsinkHarness(t)

	opts := DefaultOptions()
	opts.DSN = harness.dsn
	if err := Init(opts); err != nil {
		t.Fatalf("Init with valid DSN: %v", err)
	}

	handler := newSentryHandler()
	logger := slog.New(handler)

	logger.Warn("warn-breadcrumb-" + randomHex(t, 8))
	_ = Flush(2 * time.Second)

	pollCtx, pollCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer pollCancel()

	issues, err := harness.client.pollIssuesUntil(pollCtx, harness.projectID, func(items []issueItem) bool {
		return len(items) > 0
	}, 6)
	if err != nil {
		t.Fatalf("poll issues after warn: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues after slog Warn, got %d: %+v", len(issues), issues)
	}

	errorMsg := "error-event-" + randomHex(t, 8)
	logger.Error(errorMsg)
	if !Flush(5 * time.Second) {
		t.Fatal("Flush returned false after slog error")
	}

	issues, err = harness.client.pollIssuesUntil(pollCtx, harness.projectID, func(items []issueItem) bool {
		return len(items) == 1
	}, 30)
	if err != nil {
		t.Fatalf("poll issues after error: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 issue, got %d: %+v", len(issues), issues)
	}
	if issues[0].DigestedEventCount != 1 {
		t.Fatalf("expected digested_event_count 1, got %d", issues[0].DigestedEventCount)
	}
	if !strings.Contains(issues[0].CalculatedValue, errorMsg) {
		t.Fatalf("expected issue calculated_value to contain %q, got %q", errorMsg, issues[0].CalculatedValue)
	}
}
