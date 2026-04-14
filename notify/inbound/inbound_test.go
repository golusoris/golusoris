package inbound_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify/inbound"
)

type sink struct {
	mu     sync.Mutex
	emails []inbound.Email
}

func (s *sink) handler() inbound.HandlerFunc {
	return func(_ context.Context, m inbound.Email) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.emails = append(s.emails, m)
	}
}

func TestPostmark_parsesJSON(t *testing.T) {
	t.Parallel()
	payload := map[string]any{
		"MessageID": "abc-123",
		"Date":      "2026-04-14T10:00:00Z",
		"Subject":   "Hi",
		"FromFull":  map[string]any{"Email": "alice@example.com"},
		"ToFull":    []map[string]any{{"Email": "bot@example.com"}},
		"TextBody":  "hello",
		"HtmlBody":  "<p>hello</p>",
		"Headers":   []map[string]any{{"Name": "Received-SPF", "Value": "pass"}},
	}
	body, _ := json.Marshal(payload)
	s := &sink{}
	req := httptest.NewRequest(http.MethodPost, "/pm", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	inbound.Postmark(s.handler()).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, s.emails, 1)
	m := s.emails[0]
	require.Equal(t, "alice@example.com", m.From)
	require.Equal(t, []string{"bot@example.com"}, m.To)
	require.Equal(t, "Hi", m.Subject)
	require.Equal(t, "<p>hello</p>", m.HTML)
	require.Equal(t, "postmark", m.Provider)
	require.Contains(t, m.RawHeaders, "Received-SPF")
}

func TestSES_subscriptionConfirmation(t *testing.T) {
	t.Parallel()
	env, _ := json.Marshal(map[string]any{"Type": "SubscriptionConfirmation"})
	req := httptest.NewRequest(http.MethodPost, "/ses", strings.NewReader(string(env)))
	rec := httptest.NewRecorder()
	s := &sink{}
	inbound.SES(s.handler()).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, s.emails)
}

func TestSES_s3Action_deliversWithoutContent(t *testing.T) {
	t.Parallel()
	inner, _ := json.Marshal(map[string]any{
		"mail": map[string]any{
			"messageId":   "ses-1",
			"source":      "alice@example.com",
			"destination": []string{"bot@example.com"},
			"timestamp":   "2026-04-14T10:00:00Z",
		},
		// No content — S3 action.
	})
	env, _ := json.Marshal(map[string]any{"Type": "Notification", "Message": string(inner)})
	req := httptest.NewRequest(http.MethodPost, "/ses", strings.NewReader(string(env)))
	rec := httptest.NewRecorder()
	s := &sink{}
	inbound.SES(s.handler()).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, s.emails, 1)
	require.Equal(t, "ses-1", s.emails[0].MessageID)
	require.Equal(t, "ses", s.emails[0].Provider)
}

func TestSES_snsAction_parsesMIME(t *testing.T) {
	t.Parallel()
	raw := "From: alice@example.com\r\n" +
		"To: bot@example.com\r\n" +
		"Subject: Hi there\r\n" +
		"Message-ID: <m-1@example.com>\r\n" +
		"Date: Tue, 14 Apr 2026 10:00:00 +0000\r\n" +
		"\r\n" +
		"hello\r\n"
	inner, _ := json.Marshal(map[string]any{
		"mail":    map[string]any{"messageId": "ses-2"},
		"content": raw,
	})
	env, _ := json.Marshal(map[string]any{"Type": "Notification", "Message": string(inner)})
	req := httptest.NewRequest(http.MethodPost, "/ses", strings.NewReader(string(env)))
	rec := httptest.NewRecorder()
	s := &sink{}
	inbound.SES(s.handler()).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, s.emails, 1)
	require.Equal(t, "<m-1@example.com>", s.emails[0].MessageID)
	require.Equal(t, "Hi there", s.emails[0].Subject)
	require.Equal(t, []string{"bot@example.com"}, s.emails[0].To)
}

func TestParseMIME_roundtrip(t *testing.T) {
	t.Parallel()
	raw := []byte(
		"From: alice@example.com\r\n" +
			"To: bob@example.com, carol@example.com\r\n" +
			"Subject: =?UTF-8?B?SGVsbG8gV29ybGQ=?=\r\n" +
			"Date: Tue, 14 Apr 2026 10:00:00 +0000\r\n" +
			"\r\n" +
			"body contents\r\n")
	m, err := inbound.ParseMIME(raw)
	require.NoError(t, err)
	require.Equal(t, "Hello World", m.Subject)
	require.Len(t, m.To, 2)
	require.Contains(t, m.Text, "body contents")
}
