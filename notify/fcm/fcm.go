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
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/notify"
)

// DefaultTokenURI is Google's OAuth2 token endpoint.
const DefaultTokenURI = "https://oauth2.googleapis.com/token" //nolint:gosec // G101: public OAuth2 endpoint URL, not a credential

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
	// Clock is optional; defaults to a real wall clock. Injected for
	// testable token-expiry logic.
	Clock clockwork.Clock
}

// Sender posts notify.Messages to FCM.
type Sender struct {
	sa       ServiceAccount
	scope    string
	endpoint string
	hc       *http.Client
	clock    clockwork.Clock

	mu         sync.Mutex
	token      string
	tokenExpAt time.Time
}

// NewSender returns an FCM sender.
func NewSender(opts Options) (*Sender, error) {
	var sa ServiceAccount
	switch {
	case opts.ServiceAccount != nil:
		sa = *opts.ServiceAccount
	case len(opts.ServiceAccountJSON) > 0:
		if err := json.Unmarshal(opts.ServiceAccountJSON, &sa); err != nil {
			return nil, fmt.Errorf("notify/fcm: parse service account JSON: %w", err)
		}
	default:
		return nil, errors.New("notify/fcm: ServiceAccountJSON or ServiceAccount required")
	}
	if sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, errors.New("notify/fcm: service account missing project_id / client_email / private_key")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = DefaultTokenURI
	}
	scope := opts.Scope
	if scope == "" {
		scope = DefaultScope
	}
	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = "https://fcm.googleapis.com"
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	clk := opts.Clock
	if clk == nil {
		clk = clockwork.NewRealClock()
	}
	return &Sender{sa: sa, scope: scope, endpoint: endpoint, hc: hc, clock: clk}, nil
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

	token, err := s.accessToken(ctx)
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

		resp, err := s.hc.Do(req)
		if err != nil {
			return fmt.Errorf("notify/fcm: post: %w", err)
		}
		if resp.StatusCode/100 != 2 {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
			_ = resp.Body.Close()
			return fmt.Errorf("notify/fcm: status %d for %s: %s", resp.StatusCode, device, respBody)
		}
		_ = resp.Body.Close()
	}
	return nil
}

// accessToken returns a cached OAuth2 bearer token, refreshing when
// < 5 min remaining.
func (s *Sender) accessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock.Now()
	if s.token != "" && s.tokenExpAt.Sub(now) > 5*time.Minute {
		return s.token, nil
	}

	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(s.sa.PrivateKey))
	if err != nil {
		return "", fmt.Errorf("notify/fcm: parse private key: %w", err)
	}
	claims := jwt.MapClaims{
		"iss":   s.sa.ClientEmail,
		"scope": s.scope,
		"aud":   s.sa.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	assertion, err := tok.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("notify/fcm: sign jwt: %w", err)
	}

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.sa.TokenURI, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("notify/fcm: token req: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("notify/fcm: token post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return "", fmt.Errorf("notify/fcm: token status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("notify/fcm: token decode: %w", err)
	}
	if out.AccessToken == "" {
		return "", errors.New("notify/fcm: token response missing access_token")
	}
	s.token = out.AccessToken
	s.tokenExpAt = s.clock.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	return s.token, nil
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
