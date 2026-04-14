package bounce_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify/bounce"
)

type recorder struct {
	mu     sync.Mutex
	events []bounce.Event
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
	bounce.SES(r.handler()).ServeHTTP(rec, req)

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
	bounce.SES(r.handler()).ServeHTTP(rec, req)

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
	bounce.SES(r.handler()).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, r.events)
}

func TestSES_rejectsBadMethod(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/ses", nil)
	rec := httptest.NewRecorder()
	bounce.SES(func(context.Context, bounce.Event) {}).ServeHTTP(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
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
	bounce.Postmark(r.handler()).ServeHTTP(rec, req)

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
	bounce.Postmark(r.handler()).ServeHTTP(rec, req)

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
	bounce.Postmark(r.handler()).ServeHTTP(rec, req)

	require.False(t, r.events[0].Permanent())
}

func TestPostmark_rejectsMissingEmail(t *testing.T) {
	t.Parallel()
	payload, _ := json.Marshal(map[string]any{"Type": "HardBounce"})
	req := httptest.NewRequest(http.MethodPost, "/postmark", strings.NewReader(string(payload)))
	rec := httptest.NewRecorder()
	bounce.Postmark(func(context.Context, bounce.Event) {}).ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
