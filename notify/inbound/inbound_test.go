// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package inbound_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify/inbound"
	postmarkauth "github.com/golusoris/golusoris/notify/postmark"
)

type sink struct {
	mu     sync.Mutex
	emails []inbound.Email
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
	inbound.Postmark(acceptPostmark, s.handler()).ServeHTTP(rec, req)

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

func TestHandlersRejectNilConsumer(t *testing.T) {
	t.Parallel()
	inner, _ := json.Marshal(map[string]any{
		"mail": map[string]any{"messageId": "ses-1"},
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
			handler: inbound.SES(acceptSNS, nil),
			body:    string(sesEnvelope),
		},
		{
			name:    "Postmark",
			handler: inbound.Postmark(acceptPostmark, nil),
			body:    `{"MessageID":"postmark-1"}`,
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

func TestPostmarkRejectsUnverifiedPayloadBeforeParsing(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		body     string
		verifier postmarkauth.WebhookVerifier
	}{
		{name: "nil verifier", body: `{"MessageID":"forged"}`},
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
			s := &sink{}
			req := httptest.NewRequest(http.MethodPost, "/pm", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			inbound.Postmark(tt.verifier, s.handler()).ServeHTTP(rec, req)

			require.Equal(t, http.StatusUnauthorized, rec.Code)
			require.Empty(t, s.emails)
		})
	}
}

func TestPostmarkBodyLimitRunsBeforeVerifier(t *testing.T) {
	t.Parallel()
	const maxBodyBytes = 25 << 20
	called := false
	verify := func(*http.Request, []byte) error {
		called = true
		return nil
	}
	req := httptest.NewRequest(
		http.MethodPost,
		"/pm",
		io.LimitReader(repeatingReader('x'), maxBodyBytes+1),
	)
	rec := httptest.NewRecorder()

	inbound.Postmark(verify, (&sink{}).handler()).ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.False(t, called)
}

func TestSES_subscriptionConfirmation(t *testing.T) {
	t.Parallel()
	env, _ := json.Marshal(map[string]any{"Type": "SubscriptionConfirmation"})
	req := httptest.NewRequest(http.MethodPost, "/ses", strings.NewReader(string(env)))
	rec := httptest.NewRecorder()
	s := &sink{}
	inbound.SES(acceptSNS, s.handler()).ServeHTTP(rec, req)
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
	inbound.SES(acceptSNS, s.handler()).ServeHTTP(rec, req)

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
	inbound.SES(acceptSNS, s.handler()).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, s.emails, 1)
	require.Equal(t, "<m-1@example.com>", s.emails[0].MessageID)
	require.Equal(t, "Hi there", s.emails[0].Subject)
	require.Equal(t, []string{"bot@example.com"}, s.emails[0].To)
}

func TestSES_snsActionParsesBase64MIME(t *testing.T) {
	t.Parallel()
	raw := "From: alice@example.com\r\n" +
		"To: bot@example.com\r\n" +
		"Subject: Encoded\r\n" +
		"Message-ID: <base64@example.com>\r\n\r\n" +
		"binary-safe body\r\n"
	inner, _ := json.Marshal(map[string]any{
		"mail":    map[string]any{"messageId": "ses-base64"},
		"content": base64.StdEncoding.EncodeToString([]byte(raw)),
	})
	env, _ := json.Marshal(map[string]any{"Type": "Notification", "Message": string(inner)})
	req := httptest.NewRequest(http.MethodPost, "/ses", strings.NewReader(string(env)))
	rec := httptest.NewRecorder()
	s := &sink{}
	inbound.SES(acceptSNS, s.handler()).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, s.emails, 1)
	require.Equal(t, "<base64@example.com>", s.emails[0].MessageID)
	require.Equal(t, "Encoded", s.emails[0].Subject)
	require.Contains(t, s.emails[0].Text, "binary-safe body")
}

func TestSESRejectsUnverifiedEnvelope(t *testing.T) {
	t.Parallel()
	inner, _ := json.Marshal(map[string]any{"mail": map[string]any{"messageId": "forged"}})
	env, _ := json.Marshal(map[string]any{"Type": "Notification", "Message": string(inner)})
	req := httptest.NewRequest(http.MethodPost, "/ses", strings.NewReader(string(env)))
	rec := httptest.NewRecorder()
	s := &sink{}
	inbound.SES(nil, s.handler()).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Empty(t, s.emails)
}

func TestParseMIME_roundtrip(t *testing.T) {
	t.Parallel()
	raw := []byte(
		"From: alice@example.com\r\n" +
			"To: bob@example.com, carol@example.com\r\n" +
			"Subject: =?UTF-8?B?SGVsbG8gV29ybGQ=?=\r\n" +
			"Date: Tue, 14 Apr 2026 10:00:00 +0000\r\n" +
			"\r\n" +
			"body contents\r\n",
	)
	m, err := inbound.ParseMIME(raw)
	require.NoError(t, err)
	require.Equal(t, "Hello World", m.Subject)
	require.Len(t, m.To, 2)
	require.Contains(t, m.Text, "body contents")
}
