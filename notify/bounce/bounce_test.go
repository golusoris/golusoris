// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bounce_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify/bounce"
	postmarkauth "github.com/golusoris/golusoris/notify/postmark"
)

type recorder struct {
	mu     sync.Mutex
	events []bounce.Event
}

func acceptSNS(*http.Request, []byte) error { return nil }

func acceptPostmark(*http.Request, []byte) error { return nil }

type repeatingReader byte

func (r repeatingReader) Read(dst []byte) (int, error) {
	for i := range dst {
		dst[i] = byte(r)
	}
	return len(dst), nil
}

func (r *recorder) handler() bounce.HandlerFunc {
	return func(_ context.Context, ev bounce.Event) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, ev)
	}
}

func TestSES_bouncePermanent(t *testing.T) {
	t.Parallel()
	inner, _ := json.Marshal(map[string]any{
		"notificationType": "Bounce",
		"mail":             map[string]any{"messageId": "<msg-1>"},
		"bounce": map[string]any{
			"bounceType": "Permanent",
			"timestamp":  "2026-04-14T10:00:00Z",
			"bouncedRecipients": []map[string]any{
				{"emailAddress": "alice@example.com", "diagnosticCode": "550 mailbox not found"},
			},
		},
	})
	env, _ := json.Marshal(map[string]any{
		"Type":    "Notification",
		"Message": string(inner),
	})

	r := &recorder{}
	req := httptest.NewRequest(http.MethodPost, "/ses", strings.NewReader(string(env)))
	rec := httptest.NewRecorder()
	bounce.SES(acceptSNS, r.handler()).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, r.events, 1)
	require.Equal(t, bounce.KindBounce, r.events[0].Kind)
	require.Equal(t, "alice@example.com", r.events[0].Email)
	require.True(t, r.events[0].Permanent())
	require.Equal(t, "ses", r.events[0].Provider)
	require.Equal(t, "<msg-1>", r.events[0].MessageID)
}

func TestSES_complaint(t *testing.T) {
	t.Parallel()
	inner, _ := json.Marshal(map[string]any{
		"notificationType": "Complaint",
		"mail":             map[string]any{"messageId": "<m-2>"},
		"complaint": map[string]any{
			"complaintFeedbackType": "abuse",
			"timestamp":             "2026-04-14T11:00:00Z",
			"complainedRecipients":  []map[string]any{{"emailAddress": "bob@example.com"}},
		},
	})
	env, _ := json.Marshal(map[string]any{"Type": "Notification", "Message": string(inner)})

	r := &recorder{}
	req := httptest.NewRequest(http.MethodPost, "/ses", strings.NewReader(string(env)))
	rec := httptest.NewRecorder()
	bounce.SES(acceptSNS, r.handler()).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, r.events, 1)
	require.Equal(t, bounce.KindComplaint, r.events[0].Kind)
	require.True(t, r.events[0].Permanent())
}

func TestSES_subscriptionConfirmation(t *testing.T) {
	t.Parallel()
	env, _ := json.Marshal(map[string]any{
		"Type":         "SubscriptionConfirmation",
		"SubscribeURL": "https://sns.amazonaws.com/…",
	})
	r := &recorder{}
	req := httptest.NewRequest(http.MethodPost, "/ses", strings.NewReader(string(env)))
	rec := httptest.NewRecorder()
	bounce.SES(acceptSNS, r.handler()).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, r.events)
}

func TestSES_rejectsBadMethod(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/ses", nil)
	rec := httptest.NewRecorder()
	bounce.SES(acceptSNS, func(context.Context, bounce.Event) {}).ServeHTTP(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestSESRejectsUnverifiedEnvelope(t *testing.T) {
	t.Parallel()
	inner, _ := json.Marshal(map[string]any{"notificationType": "Delivery"})
	env, _ := json.Marshal(map[string]any{"Type": "Notification", "Message": string(inner)})
	r := &recorder{}
	req := httptest.NewRequest(http.MethodPost, "/ses", strings.NewReader(string(env)))
	rec := httptest.NewRecorder()
	bounce.SES(nil, r.handler()).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Empty(t, r.events)
}

func TestHandlersRejectNilConsumer(t *testing.T) {
	t.Parallel()
	inner, _ := json.Marshal(map[string]any{
		"notificationType": "Delivery",
		"mail":             map[string]any{"messageId": "ses-1"},
		"delivery": map[string]any{
			"recipients": []string{"alice@example.com"},
		},
	})
	sesEnvelope, _ := json.Marshal(map[string]any{
		"Type":    "Notification",
		"Message": string(inner),
	})
	tests := []struct {
		name    string
		handler http.Handler
		body    string
	}{
		{
			name:    "SES",
			handler: bounce.SES(acceptSNS, nil),
			body:    string(sesEnvelope),
		},
		{
			name:    "Postmark",
			handler: bounce.Postmark(acceptPostmark, nil),
			body:    `{"MessageID":"postmark-1","Email":"alice@example.com"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			rec := httptest.NewRecorder()

			test.handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusInternalServerError, rec.Code)
		})
	}
}

func TestPostmark_hardBounce(t *testing.T) {
	t.Parallel()
	payload := map[string]any{
		"MessageID":   "abc-123",
		"Type":        "HardBounce",
		"Email":       "alice@example.com",
		"Description": "mailbox not found",
		"CanActivate": false,
		"BouncedAt":   "2026-04-14T10:00:00Z",
	}
	body, _ := json.Marshal(payload)

	r := &recorder{}
	req := httptest.NewRequest(http.MethodPost, "/postmark", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	bounce.Postmark(acceptPostmark, r.handler()).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, r.events, 1)
	require.Equal(t, bounce.KindBounce, r.events[0].Kind)
	require.True(t, r.events[0].Permanent())
	require.Equal(t, "postmark", r.events[0].Provider)
	require.WithinDuration(t, time.Date(2026, 4, 14, 10, 0, 0, 0, time.UTC), r.events[0].Timestamp, time.Second)
}

func TestPostmark_spamComplaint(t *testing.T) {
	t.Parallel()
	payload, _ := json.Marshal(map[string]any{
		"MessageID": "x",
		"Type":      "SpamComplaint",
		"Email":     "bob@example.com",
	})
	r := &recorder{}
	req := httptest.NewRequest(http.MethodPost, "/postmark", strings.NewReader(string(payload)))
	rec := httptest.NewRecorder()
	bounce.Postmark(acceptPostmark, r.handler()).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, bounce.KindComplaint, r.events[0].Kind)
	require.True(t, r.events[0].Permanent())
}

func TestPostmark_transientNotPermanent(t *testing.T) {
	t.Parallel()
	payload, _ := json.Marshal(map[string]any{
		"MessageID":   "x",
		"Type":        "SoftBounce",
		"Email":       "c@example.com",
		"CanActivate": true,
	})
	r := &recorder{}
	req := httptest.NewRequest(http.MethodPost, "/postmark", strings.NewReader(string(payload)))
	rec := httptest.NewRecorder()
	bounce.Postmark(acceptPostmark, r.handler()).ServeHTTP(rec, req)

	require.False(t, r.events[0].Permanent())
}

func TestPostmark_rejectsMissingEmail(t *testing.T) {
	t.Parallel()
	payload, _ := json.Marshal(map[string]any{"Type": "HardBounce"})
	req := httptest.NewRequest(http.MethodPost, "/postmark", strings.NewReader(string(payload)))
	rec := httptest.NewRecorder()
	bounce.Postmark(acceptPostmark, func(context.Context, bounce.Event) {}).ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPostmarkRejectsUnverifiedPayloadBeforeParsing(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		body     string
		verifier postmarkauth.WebhookVerifier
	}{
		{name: "nil verifier", body: `{"Email":"forged@example.test"}`},
		{
			name: "failed verifier before invalid JSON parse",
			body: `{`,
			verifier: func(*http.Request, []byte) error {
				return errors.New("forged")
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &recorder{}
			req := httptest.NewRequest(http.MethodPost, "/postmark", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			bounce.Postmark(tt.verifier, r.handler()).ServeHTTP(rec, req)

			require.Equal(t, http.StatusUnauthorized, rec.Code)
			require.Empty(t, r.events)
		})
	}
}

func TestPostmarkBodyLimitRunsBeforeVerifier(t *testing.T) {
	t.Parallel()
	const maxBodyBytes = 1 << 20
	called := false
	verify := func(*http.Request, []byte) error {
		called = true
		return nil
	}
	req := httptest.NewRequest(
		http.MethodPost,
		"/postmark",
		io.LimitReader(repeatingReader('x'), maxBodyBytes+1),
	)
	rec := httptest.NewRecorder()

	bounce.Postmark(verify, (&recorder{}).handler()).ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.False(t, called)
}
