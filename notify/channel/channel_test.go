// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package channel_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/channel"
	"github.com/golusoris/golusoris/webhooks/out"
)

func sampleConfig() channel.Config {
	return channel.Config{
		WebhookURL: "https://discord.com/api/webhooks/123/token-secret",
		URL:        "https://webhook.site/api/v1/hook?token=webhook-secret-token",
		ServerURL:  "https://admin:userinfo-secret-pass@ntfy.example.com/alerts",
		Token:      "auth-token-12345",
		BotToken:   "bot-token-67890",
		AppToken:   "app-token-abcde",
		Password:   "top-secret-pass",
		Secret:     "hmac-key-secret",
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()
	cfg := sampleConfig()

	cases := []struct {
		name       string
		err        error
		secretText string
	}{
		{name: "nil error", err: nil, secretText: ""},
		{name: "webhook url", err: errors.New("post https://discord.com/api/webhooks/123/token-secret failed: 500"), secretText: "https://discord.com/api/webhooks/123/token-secret"},
		{name: "url with token", err: errors.New("dial error to https://webhook.site/api/v1/hook?token=webhook-secret-token: eof"), secretText: "https://webhook.site/api/v1/hook?token=webhook-secret-token"},
		{name: "server url userinfo password", err: errors.New("basic auth failed with password userinfo-secret-pass"), secretText: "userinfo-secret-pass"},
		{name: "server url full userinfo", err: errors.New("connection failed for admin:userinfo-secret-pass"), secretText: "admin:userinfo-secret-pass"},
		{name: "token", err: errors.New("invalid auth with auth-token-12345: rejected"), secretText: "auth-token-12345"},
		{name: "bot token", err: errors.New("telegram error with bot-token-67890: unauthorized"), secretText: "bot-token-67890"},
		{name: "app token", err: errors.New("gotify error with app-token-abcde: bad request"), secretText: "app-token-abcde"},
		{name: "password", err: errors.New("authentication failed for top-secret-pass"), secretText: "top-secret-pass"},
		{name: "secret", err: errors.New("signature mismatch using hmac-key-secret"), secretText: "hmac-key-secret"},
		{name: "clean error untouched", err: errors.New("connection timeout to remote host"), secretText: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := channel.Redact(tc.err, cfg)
			assertRedacted(t, tc.err, got, tc.secretText)
		})
	}
}

func assertRedacted(t *testing.T, err error, got, secretText string) {
	t.Helper()
	if err == nil {
		if got != "" {
			t.Errorf("Redact(nil) = %q, want empty", got)
		}
		return
	}
	if secretText != "" {
		if strings.Contains(got, secretText) {
			t.Errorf("Redact() failed to redact secret %q, got: %q", secretText, got)
		}
		if !strings.Contains(got, "[REDACTED]") {
			t.Errorf("Redact() missing [REDACTED] in %q", got)
		}
		return
	}
	if got != err.Error() {
		t.Errorf("Redact() altered clean error: got %q, want %q", got, err.Error())
	}
}

func TestRedact_EmptySecretsIgnored(t *testing.T) {
	t.Parallel()
	emptyCfg := channel.Config{}
	err := errors.New("something went wrong")
	got := channel.Redact(err, emptyCfg)
	if got != "something went wrong" {
		t.Fatalf("Redact with empty config modified error: got %q", got)
	}
}

func TestSender_AllChatKindsBuild(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind channel.Kind
		cfg  channel.Config
		name string
	}{
		{
			kind: channel.Discord,
			cfg:  channel.Config{WebhookURL: "https://discord.com/api/webhooks/123/token"},
			name: "discord",
		},
		{
			kind: channel.Slack,
			cfg:  channel.Config{WebhookURL: "https://hooks.slack.com/services/T/B/X"},
			name: "slack",
		},
		{
			kind: channel.Telegram,
			cfg:  channel.Config{BotToken: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11", ChatID: "-1001234567890"},
			name: "telegram",
		},
		{
			kind: channel.Ntfy,
			cfg:  channel.Config{ServerURL: "https://ntfy.sh", Topic: "alerts"},
			name: "ntfy",
		},
		{
			kind: channel.Gotify,
			cfg:  channel.Config{ServerURL: "https://gotify.example.com", AppToken: "A12345678901234"},
			name: "gotify",
		},
		{
			kind: channel.Teams,
			cfg:  channel.Config{WebhookURL: "https://outlook.office.com/webhook/123"},
			name: "teams",
		},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()
			s, err := channel.Sender(tc.kind, tc.cfg)
			if err != nil {
				t.Fatalf("Sender(%s) unexpected error: %v", tc.kind, err)
			}
			if s == nil {
				t.Fatalf("Sender(%s) returned nil sender", tc.kind)
			}
			if s.Name() != tc.name {
				t.Errorf("Sender(%s).Name() = %q, want %q", tc.kind, s.Name(), tc.name)
			}
		})
	}
}

func TestSender_Errors(t *testing.T) {
	t.Parallel()
	t.Run("unknown kind", func(t *testing.T) {
		t.Parallel()
		_, err := channel.Sender("unsupported_kind", channel.Config{})
		if !errors.Is(err, channel.ErrUnsupportedKind) {
			t.Fatalf("want ErrUnsupportedKind, got %v", err)
		}
	})

	t.Run("webhook kind via sender", func(t *testing.T) {
		t.Parallel()
		_, err := channel.Sender(channel.Webhook, channel.Config{})
		if !errors.Is(err, channel.ErrWebhookNotChatSender) {
			t.Fatalf("want ErrWebhookNotChatSender, got %v", err)
		}
	})
}

func TestSender_MissingRequiredOptions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		kind channel.Kind
		cfg  channel.Config
	}{
		{name: "discord missing webhook url", kind: channel.Discord, cfg: channel.Config{}},
		{name: "slack missing webhook url", kind: channel.Slack, cfg: channel.Config{}},
		{name: "telegram missing bot token", kind: channel.Telegram, cfg: channel.Config{ChatID: "123"}},
		{name: "ntfy missing server url", kind: channel.Ntfy, cfg: channel.Config{Topic: "alerts"}},
		{name: "ntfy missing topic", kind: channel.Ntfy, cfg: channel.Config{ServerURL: "https://ntfy.sh"}},
		{name: "gotify missing server url", kind: channel.Gotify, cfg: channel.Config{AppToken: "tok"}},
		{name: "gotify missing app token", kind: channel.Gotify, cfg: channel.Config{ServerURL: "https://gotify.com"}},
		{name: "teams missing webhook url", kind: channel.Teams, cfg: channel.Config{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := channel.Sender(tc.kind, tc.cfg)
			if err == nil {
				t.Fatalf("Sender(%s) expected error on missing required field, got nil", tc.kind)
			}
		})
	}
}

func TestDeliver_Chat(t *testing.T) {
	t.Parallel()
	var (
		mu       sync.Mutex
		received []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = b
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cfg := channel.Config{WebhookURL: srv.URL}
	msg := notify.Message{Subject: "Deploy finished", Body: "Commit abc deployed to staging"}
	if err := channel.Deliver(t.Context(), channel.Discord, cfg, msg, channel.WebhookRequest{}); err != nil {
		t.Fatalf("Deliver chat unexpected error: %v", err)
	}

	mu.Lock()
	got := string(received)
	mu.Unlock()
	if !strings.Contains(got, "Commit abc deployed to staging") {
		t.Fatalf("expected payload to contain body, got: %s", got)
	}

	// Subject only fallback
	msgSub := notify.Message{Subject: "Subject only"}
	if err := channel.Deliver(t.Context(), channel.Discord, cfg, msgSub, channel.WebhookRequest{}); err != nil {
		t.Fatalf("Deliver chat subject only: %v", err)
	}

	// Text only fallback
	msgText := notify.Message{Text: "Text only"}
	if err := channel.Deliver(t.Context(), channel.Discord, cfg, msgText, channel.WebhookRequest{}); err != nil {
		t.Fatalf("Deliver chat text only: %v", err)
	}

	// Send error path
	badCfg := channel.Config{WebhookURL: "http://127.0.0.1:1/nonexistent"}
	if err := channel.Deliver(t.Context(), channel.Discord, badCfg, msg, channel.WebhookRequest{}); err == nil {
		t.Fatal("Deliver to bad URL: expected error, got nil")
	}

	// Unknown kind returns error wrapping ErrUnsupportedKind.
	err := channel.Deliver(t.Context(), "invalid_kind", cfg, msg, channel.WebhookRequest{})
	if !errors.Is(err, channel.ErrUnsupportedKind) {
		t.Fatalf("Deliver unknown kind: want ErrUnsupportedKind, got %v", err)
	}
}

func TestDeliverWebhook_DefaultMessage(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cfg := channel.Config{
		URL:                 srv.URL,
		AllowPrivateNetwork: true,
		AllowInsecureHTTP:   true,
	}
	hook := channel.WebhookRequest{Event: "hook_event"}
	if err := channel.Deliver(t.Context(), channel.Webhook, cfg, notify.Message{Subject: "s"}, hook); err != nil {
		t.Fatalf("Deliver webhook with default message: %v", err)
	}
	if err := channel.Deliver(t.Context(), channel.Webhook, cfg, notify.Message{}, channel.WebhookRequest{}); !errors.Is(err, channel.ErrNoEvent) {
		t.Fatalf("Deliver webhook without event: got %v, want ErrNoEvent", err)
	}
}

type memoryStore struct {
	mu         sync.Mutex
	deliveries []out.Delivery
}

func (m *memoryStore) SaveEndpoint(_ context.Context, _ out.Endpoint) error { return nil }
func (m *memoryStore) FindEndpoint(_ context.Context, _ string) (out.Endpoint, error) {
	return out.Endpoint{}, errors.New("not found")
}

func (m *memoryStore) ListEndpoints(_ context.Context, _ string) ([]out.Endpoint, error) {
	return nil, nil
}
func (m *memoryStore) DeleteEndpoint(_ context.Context, _ string) error { return nil }
func (m *memoryStore) SaveDelivery(_ context.Context, d out.Delivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deliveries = append(m.deliveries, d)
	return nil
}

func (m *memoryStore) FindDelivery(_ context.Context, _ string) (out.Delivery, error) {
	return out.Delivery{}, errors.New("not found")
}
func (m *memoryStore) ListDeadLetters(_ context.Context) ([]out.Delivery, error) { return nil, nil }

func verifyWebhookSignature(t *testing.T, secret string, body []byte, sig string) {
	t.Helper()
	if len(body) == 0 {
		t.Fatal("webhook server received empty body")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	wantSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if sig != wantSig {
		t.Fatalf("X-Webhook-Signature = %q, want %q", sig, wantSig)
	}
}

func TestDeliverWebhook_Success(t *testing.T) {
	t.Parallel()
	const secret = "webhook-test-signing-key"
	var (
		mu           sync.Mutex
		receivedBody []byte
		receivedSig  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		receivedSig = r.Header.Get("X-Webhook-Signature")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cfg := channel.Config{
		URL:                 srv.URL,
		Secret:              secret,
		AllowPrivateNetwork: true,
		AllowInsecureHTTP:   true,
	}
	store := &memoryStore{}
	req := channel.WebhookRequest{
		EndpointID: "test-endpoint-1",
		Event:      "circuit_opened",
		Payload:    map[string]string{"status": "tripped"},
		Store:      store,
		Clock:      clock.NewFake(),
	}
	if err := channel.Deliver(t.Context(), channel.Webhook, cfg, notify.Message{}, req); err != nil {
		t.Fatalf("Deliver webhook: %v", err)
	}

	mu.Lock()
	body := append([]byte(nil), receivedBody...)
	sig := receivedSig
	mu.Unlock()

	verifyWebhookSignature(t, secret, body, sig)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.deliveries) == 0 {
		t.Fatal("expected delivery to be saved to store")
	}
}

func TestDeliverWebhook_3AttemptsThenError(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	cfg := channel.Config{
		URL:                 srv.URL,
		AllowPrivateNetwork: true,
		AllowInsecureHTTP:   true,
	}
	req := channel.WebhookRequest{
		EndpointID: "failing-ep",
		Event:      "alert",
		Payload:    "something",
		Options: channel.WebhookOptions{
			MaxAttempts: 3,
			Backoff:     func(int) time.Duration { return 0 },
		},
	}

	err := channel.DeliverWebhook(t.Context(), cfg, req)
	if err == nil {
		t.Fatal("expected error after 3 failed attempts, got nil")
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("expected exactly 3 attempts, got %d", got)
	}
}

func TestConfig_Methods(t *testing.T) {
	t.Parallel()
	cfg := sampleConfig()
	if !cfg.HasSecret() {
		t.Fatal("expected HasSecret() = true for sample config")
	}

	redacted := cfg.Redacted()
	if redacted.HasSecret() {
		t.Fatal("expected HasSecret() = false after Redacted()")
	}
	if redacted.ServerURL != cfg.ServerURL {
		t.Fatalf("Redacted() changed public ServerURL: %q vs %q", redacted.ServerURL, cfg.ServerURL)
	}

	merged := redacted.MergeSecrets(cfg)
	if !merged.HasSecret() {
		t.Fatal("expected HasSecret() = true after MergeSecrets()")
	}
	if merged.Token != cfg.Token {
		t.Fatalf("MergeSecrets() did not restore Token: %q vs %q", merged.Token, cfg.Token)
	}
}

func TestKind_Valid(t *testing.T) {
	t.Parallel()
	validKinds := []channel.Kind{
		channel.Discord, channel.Slack, channel.Telegram,
		channel.Ntfy, channel.Gotify, channel.Teams, channel.Webhook,
	}
	for _, k := range validKinds {
		if !k.Valid() {
			t.Errorf("expected Kind(%s).Valid() = true", k)
		}
	}
	if channel.Kind("").Valid() {
		t.Error("expected empty kind to be invalid")
	}
	if channel.Kind("email").Valid() {
		t.Error("expected email kind to be invalid")
	}
}
