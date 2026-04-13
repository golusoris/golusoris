package in_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/webhooks/in"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// --- GitHub ---

func githubSig(secret, body string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}

func TestGitHub_valid(t *testing.T) {
	const secret = "mysecret"
	body := `{"action":"push"}`
	handler := in.GitHub(secret)(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubSig(secret, body))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
}

func TestGitHub_invalid(t *testing.T) {
	handler := in.GitHub("secret")(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body"))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

func TestGitHub_missingHeader(t *testing.T) {
	handler := in.GitHub("secret")(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body"))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

// --- Stripe ---

func stripeSig(secret, body string, ts int64) string {
	payload := fmt.Sprintf("%d.%s", ts, body)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	mac := hex.EncodeToString(h.Sum(nil))
	return fmt.Sprintf("t=%d,v1=%s", ts, mac)
}

func TestStripe_valid(t *testing.T) {
	const secret = "whsec_test"
	body := `{"type":"charge.succeeded"}`
	ts := time.Now().Unix()
	handler := in.Stripe(secret)(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", stripeSig(secret, body, ts))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
}

func TestStripe_oldTimestamp(t *testing.T) {
	const secret = "whsec_test"
	body := `{}`
	ts := time.Now().Add(-10 * time.Minute).Unix()
	handler := in.Stripe(secret)(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Stripe-Signature", stripeSig(secret, body, ts))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

// --- Slack ---

func slackSig(secret, body string, ts int64) string {
	base := fmt.Sprintf("v0:%d:%s", ts, body)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(base))
	return "v0=" + hex.EncodeToString(h.Sum(nil))
}

func TestSlack_valid(t *testing.T) {
	const secret = "slack_secret"
	body := `payload=test`
	ts := time.Now().Unix()
	handler := in.Slack(secret)(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", fmt.Sprintf("%d", ts))
	req.Header.Set("X-Slack-Signature", slackSig(secret, body, ts))
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
}

// --- Generic HMAC ---

func TestHMAC_valid(t *testing.T) {
	const secret = "genericsecret"
	body := `{"event":"test"}`
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(body))
	sig := "sha256=" + hex.EncodeToString(h.Sum(nil))

	handler := in.HMAC(secret, "X-My-Signature")(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("X-My-Signature", sig)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
}
