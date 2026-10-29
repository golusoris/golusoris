// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package fcm sends push notifications via Firebase Cloud Messaging
// HTTP v1 API.
//
// Auth is the standard Google service-account flow: a JWT signed with
// the account's RSA private key is exchanged for an OAuth2 access
// token; the token is cached until ~5 min before its expiry.
//
// Usage:
//
//	saJSON, _ := os.ReadFile("service-account.json")
//	s, err := fcm.NewSender(fcm.Options{
//	    ServiceAccountJSON: saJSON,
//	})
//	notify.New(logger, notify.WithSender(s))
//
// Message routing: each recipient in [notify.Message.To] is a device
// registration token. The notification title is [notify.Message.Subject];
// the body is [notify.Message.Body] (or [notify.Message.Text]).
// [notify.Message.Metadata] becomes the FCM `data` payload
// (string values only).
package fcm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/core/validate"
	httpclient "github.com/golusoris/golusoris/httpx/client"
	"github.com/golusoris/golusoris/notify"
)

const (
	defaultRequestTimeout   = 10 * time.Second
	defaultMaxResponseBytes = 1 << 20
)

// DefaultTokenURI is Google's OAuth2 token endpoint.
const DefaultTokenURI = "https://oauth2.googleapis.com/token" // #nosec G101 -- public OAuth2 endpoint URL, not a credential

// DefaultScope is the FCM v1 send scope.
const DefaultScope = "https://www.googleapis.com/auth/firebase.messaging"

// ServiceAccount mirrors the relevant fields of a Google service-account
// JSON key. Apps usually pass the raw JSON bytes via [Options.ServiceAccountJSON]
// and let [NewSender] parse them.
type ServiceAccount struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	PrivateKey  string `json:"private_key"` // PEM-encoded RSA
	ClientEmail string `json:"client_email"`
	TokenURI    string `json:"token_uri"`
}

// Options configures the FCM sender.
type Options struct {
	// ServiceAccountJSON is the raw bytes of the service-account key
	// JSON. Exactly one of ServiceAccountJSON or ServiceAccount must
	// be set.
	ServiceAccountJSON []byte `koanf:"service_account_json"`
	// ServiceAccount lets callers inject a pre-parsed struct (handy in
	// tests or when loading from secrets/).
	ServiceAccount *ServiceAccount
	// Scope overrides the default OAuth scope.
	Scope string `koanf:"scope"`
	// Endpoint overrides the FCM send URL root (tests).
	Endpoint string `koanf:"endpoint"`
	// HTTPClient is optional; defaults to a 10s-timeout client.
	HTTPClient *http.Client
	// MaxResponseBytes caps decoded OAuth token responses. Zero defaults to 1 MiB;
	// negative and unbounded values are rejected.
	MaxResponseBytes int64 `koanf:"max_response_bytes"`
	// Clock is optional; defaults to a real wall clock. Injected for
	// testable token-expiry logic.
	Clock clockwork.Clock
}

// Sender posts notify.Messages to FCM.
type Sender struct {
	sa               ServiceAccount
	endpoint         string
	hc               *http.Client
	maxResponseBytes int64
	clock            clockwork.Clock
	conf             jwt.Config

	mu     sync.Mutex
	tok    *oauth2.Token
	tokExp time.Time
}

// NewSender returns an FCM sender.
func NewSender(opts Options) (*Sender, error) {
	sa, err := resolveServiceAccount(opts)
	if err != nil {
		return nil, err
	}
	scope := opts.Scope
	if scope == "" {
		scope = DefaultScope
	}
	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = "https://fcm.googleapis.com"
	}
	if opts.MaxResponseBytes < 0 || opts.MaxResponseBytes == math.MaxInt64 {
		return nil, errors.New("notify/fcm: max_response_bytes must be zero or a positive bounded value")
	}
	maxResponseBytes := opts.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}
	hc := httpclient.CloneBounded(opts.HTTPClient, defaultRequestTimeout)

	clk := opts.Clock
	if validate.IsNil(clk) {
		clk = clockwork.NewRealClock()
	}

	return &Sender{
		sa:               sa,
		endpoint:         endpoint,
		hc:               hc,
		maxResponseBytes: maxResponseBytes,
		clock:            clk,
		conf: jwt.Config{
			Email:      sa.ClientEmail,
			PrivateKey: []byte(sa.PrivateKey),
			Scopes:     []string{scope},
			TokenURL:   sa.TokenURI,
		},
	}, nil
}

// resolveServiceAccount picks the account from Options (either
// pre-parsed or raw JSON) and validates the fields the OAuth2
// JWT-bearer flow requires.
func resolveServiceAccount(opts Options) (ServiceAccount, error) {
	sa, err := serviceAccountFrom(opts)
	if err != nil {
		return ServiceAccount{}, err
	}
	if sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return ServiceAccount{}, errors.New("notify/fcm: service account missing project_id / client_email / private_key")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = DefaultTokenURI
	}
	return sa, nil
}

// serviceAccountFrom implements the "exactly one of ServiceAccount or
// ServiceAccountJSON" contract documented on [Options].
func serviceAccountFrom(opts Options) (ServiceAccount, error) {
	hasAccount := opts.ServiceAccount != nil
	hasJSON := len(opts.ServiceAccountJSON) > 0
	if hasAccount == hasJSON {
		if hasAccount {
			return ServiceAccount{}, errors.New("notify/fcm: exactly one of ServiceAccountJSON or ServiceAccount must be set")
		}
		return ServiceAccount{}, errors.New("notify/fcm: ServiceAccountJSON or ServiceAccount required")
	}
	switch {
	case hasAccount:
		return *opts.ServiceAccount, nil
	case hasJSON:
		var sa ServiceAccount
		if err := json.Unmarshal(opts.ServiceAccountJSON, &sa); err != nil {
			return ServiceAccount{}, fmt.Errorf("notify/fcm: parse service account JSON: %w", err)
		}
		return sa, nil
	}
	return ServiceAccount{}, errors.New("notify/fcm: unreachable service account source")
}

// Name implements [notify.Sender].
func (s *Sender) Name() string { return "fcm" }

// Send implements [notify.Sender]. Each recipient in msg.To gets a
// separate FCM request (the v1 API is single-recipient; use topic
// subscriptions for fan-out).
func (s *Sender) Send(ctx context.Context, msg notify.Message) error {
	if len(msg.To) == 0 {
		return errors.New("notify/fcm: at least one device token required")
	}
	body := msg.Body
	if body == "" {
		body = msg.Text
	}
	if msg.Subject == "" && body == "" {
		return errors.New("notify/fcm: subject or body required")
	}

	tok, err := s.accessToken(ctx)
	if err != nil {
		return err
	}

	target := s.endpoint + "/v1/projects/" + url.PathEscape(s.sa.ProjectID) + "/messages:send"

	for _, device := range msg.To {
		payload := fcmSendRequest{
			Message: fcmMessage{
				Token: device,
				Notification: &fcmNotification{
					Title: msg.Subject,
					Body:  body,
				},
				Data: msg.Metadata,
			},
		}
		if err := s.sendOne(ctx, target, tok.AccessToken, device, payload); err != nil {
			return err
		}
	}
	return nil
}

// sendOne posts a single FCM v1 message and maps non-2xx to an error.
func (s *Sender) sendOne(ctx context.Context, target, token, device string, payload fcmSendRequest) (err error) {
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify/fcm: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("notify/fcm: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.hc.Do(req) //nolint:bodyclose // closed via gerr.CloseInto on the deferred line below
	if err != nil {
		return fmt.Errorf("notify/fcm: post: %w", err)
	}
	defer gerr.CloseInto(resp.Body, &err, "notify/fcm: close response body")
	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("notify/fcm: status %d for %s: %s", resp.StatusCode, device, respBody)
	}
	return nil
}

func (s *Sender) accessToken(ctx context.Context) (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.tok != nil && s.tokExp.Sub(s.clock.Now()) > 5*time.Minute {
		return s.tok, nil
	}
	now := s.clock.Now()
	tok, err := s.fetchToken(ctx)
	if err != nil {
		return nil, err
	}
	s.tok = tok
	s.tokExp = tokenExpiry(now, tok)
	return tok, nil
}

func tokenExpiry(now time.Time, tok *oauth2.Token) time.Time {
	if tok == nil {
		return time.Time{}
	}
	// x/oauth2/jwt keeps the raw JSON body as Extra, so numbers decode as float64.
	if v, ok := tok.Extra("expires_in").(float64); ok && v > 0 {
		return now.Add(time.Duration(v) * time.Second)
	}
	return time.Time{}
}

func (s *Sender) fetchToken(ctx context.Context) (*oauth2.Token, error) {
	ctx, cancel := context.WithTimeout(ctx, s.hc.Timeout)
	defer cancel()
	limiter := &tokenLimiterTransport{
		base:     s.hc.Transport,
		maxBytes: s.maxResponseBytes,
	}
	tokenHC := *s.hc
	tokenHC.Transport = limiter

	ctx2 := context.WithValue(ctx, oauth2.HTTPClient, &tokenHC)
	// x/oauth2/jwt v0.37.0 uses PostForm without attaching its token-source context.
	limiter.withContext = func(req *http.Request) *http.Request { return req.Clone(ctx2) }
	tok, err := s.conf.TokenSource(ctx2).Token()
	if err != nil {
		if limiter.bodyTooLarge {
			err = httpclient.ErrBodyTooLarge
		} else if ctxErr := ctx2.Err(); ctxErr != nil {
			err = ctxErr
		}
		return nil, fmt.Errorf("notify/fcm: token: %w", err)
	}
	if tok == nil || tok.AccessToken == "" {
		return nil, errors.New("notify/fcm: token response missing access_token")
	}
	return tok, nil
}

// tokenLimiterTransport enforces maxResponseBytes on successful token responses.
type tokenLimiterTransport struct {
	base         http.RoundTripper
	withContext  func(*http.Request) *http.Request
	maxBytes     int64
	bodyTooLarge bool
}

func (t *tokenLimiterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(t.withContext(req))
	if err != nil {
		return nil, fmt.Errorf("notify/fcm: round trip: %w", err)
	}
	if resp != nil && resp.Body != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		resp.Body = &limitReadCloser{
			rc:       resp.Body,
			maxBytes: t.maxBytes,
			onLimit: func() {
				t.bodyTooLarge = true
			},
		}
	}
	return resp, nil
}

// CloseIdleConnections forwards idle pool cleanup when the base transport supports it.
func (t *tokenLimiterTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// limitReadCloser tracks body bytes and fails when exceeding maxBytes.
type limitReadCloser struct {
	rc       io.ReadCloser
	maxBytes int64
	read     int64
	onLimit  func()
}

func (l *limitReadCloser) Read(p []byte) (int, error) {
	n, err := l.rc.Read(p)
	l.read += int64(n)
	if l.read > l.maxBytes {
		if l.onLimit != nil {
			l.onLimit()
		}
		return n, fmt.Errorf("notify/fcm: read token body: %w: limit %d", httpclient.ErrBodyTooLarge, l.maxBytes)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("notify/fcm: read token body: %w", err)
	}
	return n, err //nolint:wrapcheck // io.EOF must propagate verbatim to readers
}

func (l *limitReadCloser) Close() error {
	if err := l.rc.Close(); err != nil {
		return fmt.Errorf("notify/fcm: close token body: %w", err)
	}
	return nil
}

type fcmSendRequest struct {
	Message fcmMessage `json:"message"`
}

type fcmMessage struct {
	Token        string            `json:"token,omitempty"`
	Topic        string            `json:"topic,omitempty"`
	Notification *fcmNotification  `json:"notification,omitempty"`
	Data         map[string]string `json:"data,omitempty"`
}

type fcmNotification struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}
