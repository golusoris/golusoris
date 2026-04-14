// Package apns2 sends Apple push notifications via APNs HTTP/2 with
// token-based auth (.p8 key + team ID + key ID).
//
// The bearer JWT is ES256-signed per APNs spec and cached in memory
// for ~50 minutes (APNs accepts tokens up to 60 min old); concurrent
// sends share a single token.
//
// Usage:
//
//	p8Key, _ := os.ReadFile("AuthKey_ABC.p8")
//	s, err := apns2.NewSender(apns2.Options{
//	    KeyID:  "ABC123DEF",
//	    TeamID: "TEAM123",
//	    Topic:  "com.example.app",
//	    P8Key:  p8Key,
//	})
//	notify.New(logger, notify.WithSender(s))
//
// Message routing: each recipient in [notify.Message.To] is an APNs
// device token (hex). [notify.Message.Subject] → alert title,
// [notify.Message.Body] → alert body, [notify.Message.Metadata] → custom
// top-level fields in the payload.
package apns2

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jonboulle/clockwork"
	"golang.org/x/net/http2"

	"github.com/golusoris/golusoris/notify"
)

// APNs hosts.
const (
	ProductionHost = "https://api.push.apple.com"
	SandboxHost    = "https://api.sandbox.push.apple.com"
)

// PushType categorises the notification per APNs spec. Apple requires
// it on iOS 13+ / macOS 10.15+.
type PushType string

// PushType values.
const (
	PushTypeAlert        PushType = "alert"
	PushTypeBackground   PushType = "background"
	PushTypeVoIP         PushType = "voip"
	PushTypeComplication PushType = "complication"
	PushTypeFileProvider PushType = "fileprovider"
	PushTypeMDM          PushType = "mdm"
)

// Priority is APNs' apns-priority header. 10 = immediate; 5 = throttled.
type Priority int

// Priority values.
const (
	PriorityImmediate Priority = 10
	PriorityLow       Priority = 5
)

// Options configures the APNs sender.
type Options struct {
	// KeyID is the 10-char APNs auth key ID (AuthKey_<KeyID>.p8). Required.
	KeyID string `koanf:"key_id"`
	// TeamID is the 10-char Apple Developer team ID. Required.
	TeamID string `koanf:"team_id"`
	// Topic is the app's bundle ID (iOS) or bundle ID suffix (VoIP /
	// complication). Required; used as apns-topic header.
	Topic string `koanf:"topic"`
	// P8Key is the PEM-encoded PKCS8 ECDSA P-256 private key (the
	// contents of the .p8 file Apple gives you). Required.
	P8Key []byte `koanf:"p8_key"`
	// Production selects production hosts. Default false = sandbox.
	Production bool `koanf:"production"`
	// DefaultPushType defaults to [PushTypeAlert] when
	// msg.Metadata["apns-push-type"] is not set.
	DefaultPushType PushType `koanf:"default_push_type"`
	// DefaultPriority defaults to [PriorityImmediate].
	DefaultPriority Priority `koanf:"default_priority"`
	// Expiration sets apns-expiration when > 0; 0 means "store and
	// forward" (APNs default: one attempt, drop on failure).
	Expiration time.Duration `koanf:"expiration"`
	// HTTPClient is optional; NewSender will create an HTTP/2-capable
	// client when this is nil.
	HTTPClient *http.Client
	// Clock is optional; defaults to a real wall clock. Injected for
	// testable JWT-expiry logic.
	Clock clockwork.Clock
}

// ErrUnregistered is returned when APNs reports the device token is no
// longer valid (HTTP 410 or reason=Unregistered in the body). Apps
// should delete the device from their store.
var ErrUnregistered = errors.New("notify/apns2: device unregistered")

// Sender posts notify.Messages to APNs.
type Sender struct {
	opts  Options
	key   *ecdsa.PrivateKey
	host  string
	hc    *http.Client
	clock clockwork.Clock

	mu           sync.Mutex
	cachedJWT    string
	jwtGenerated time.Time
}

// NewSender returns an APNs sender.
func NewSender(opts Options) (*Sender, error) {
	if opts.KeyID == "" || opts.TeamID == "" || opts.Topic == "" || len(opts.P8Key) == 0 {
		return nil, errors.New("notify/apns2: KeyID, TeamID, Topic and P8Key are required")
	}
	block, _ := pem.Decode(opts.P8Key)
	if block == nil {
		return nil, errors.New("notify/apns2: P8Key is not valid PEM")
	}
	anyKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("notify/apns2: parse p8: %w", err)
	}
	key, ok := anyKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("notify/apns2: p8 key is not ECDSA")
	}
	host := SandboxHost
	if opts.Production {
		host = ProductionHost
	}
	hc := opts.HTTPClient
	if hc == nil {
		// APNs requires HTTP/2. Go's stdlib negotiates h2 by default
		// with TLS; the explicit http2 transport enables h2 without
		// relying on that negotiation.
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		}
		if err := http2.ConfigureTransport(tr); err != nil {
			return nil, fmt.Errorf("notify/apns2: configure h2: %w", err)
		}
		hc = &http.Client{Transport: tr, Timeout: 15 * time.Second}
	}
	if opts.DefaultPushType == "" {
		opts.DefaultPushType = PushTypeAlert
	}
	if opts.DefaultPriority == 0 {
		opts.DefaultPriority = PriorityImmediate
	}
	clk := opts.Clock
	if clk == nil {
		clk = clockwork.NewRealClock()
	}
	return &Sender{opts: opts, key: key, host: host, hc: hc, clock: clk}, nil
}

// Name implements [notify.Sender].
func (s *Sender) Name() string { return "apns2" }

// Send implements [notify.Sender]. Each recipient in msg.To gets a
// separate APNs request (the spec is single-token per request).
func (s *Sender) Send(ctx context.Context, msg notify.Message) error {
	if len(msg.To) == 0 {
		return errors.New("notify/apns2: at least one device token required")
	}
	body := msg.Body
	if body == "" {
		body = msg.Text
	}
	if msg.Subject == "" && body == "" {
		return errors.New("notify/apns2: subject or body required")
	}

	token, err := s.authJWT()
	if err != nil {
		return err
	}

	payload, err := buildPayload(msg, body)
	if err != nil {
		return err
	}

	pushType := s.opts.DefaultPushType
	if v := msg.Metadata["apns-push-type"]; v != "" {
		pushType = PushType(v)
	}
	priority := s.opts.DefaultPriority
	if v := msg.Metadata["apns-priority"]; v != "" {
		if p, perr := strconv.Atoi(v); perr == nil {
			priority = Priority(p)
		}
	}

	for _, device := range msg.To {
		req, rerr := s.newDeviceRequest(ctx, device, payload, token, pushType, priority, msg.Metadata)
		if rerr != nil {
			return rerr
		}
		if err := s.do(req, device); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sender) newDeviceRequest(
	ctx context.Context, device string, payload []byte, token string,
	pushType PushType, priority Priority, metadata map[string]string,
) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.host+"/3/device/"+device, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("notify/apns2: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Apns-Topic", s.opts.Topic)
	req.Header.Set("Apns-Push-Type", string(pushType))
	req.Header.Set("Apns-Priority", strconv.Itoa(int(priority)))
	if s.opts.Expiration > 0 {
		exp := s.clock.Now().Add(s.opts.Expiration).Unix()
		req.Header.Set("Apns-Expiration", strconv.FormatInt(exp, 10))
	}
	if id := metadata["apns-id"]; id != "" {
		req.Header.Set("Apns-Id", id)
	}
	if cid := metadata["apns-collapse-id"]; cid != "" {
		req.Header.Set("Apns-Collapse-Id", cid)
	}
	return req, nil
}

func (s *Sender) do(req *http.Request, device string) error {
	resp, err := s.hc.Do(req) //nolint:gosec // G107 SSRF: device path segment is caller-controlled; callers are trusted (their app's stored tokens) // #nosec G704
	if err != nil {
		return fmt.Errorf("notify/apns2: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode == http.StatusGone || bytes.Contains(raw, []byte(`"Unregistered"`)) {
		return fmt.Errorf("%w: device=%s (APNs %d: %s)", ErrUnregistered, device, resp.StatusCode, raw)
	}
	return fmt.Errorf("notify/apns2: status %d for %s: %s", resp.StatusCode, device, raw)
}

// authJWT returns a cached ES256 JWT, regenerating when older than 50
// minutes (APNs accepts tokens up to 60 min per spec).
func (s *Sender) authJWT() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	if s.cachedJWT != "" && now.Sub(s.jwtGenerated) < 50*time.Minute {
		return s.cachedJWT, nil
	}

	claims := jwt.MapClaims{
		"iss": s.opts.TeamID,
		"iat": now.Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	tok.Header["kid"] = s.opts.KeyID
	signed, err := tok.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("notify/apns2: sign jwt: %w", err)
	}
	s.cachedJWT = signed
	s.jwtGenerated = now
	return signed, nil
}

func buildPayload(msg notify.Message, body string) ([]byte, error) {
	alert := apsAlert{Title: msg.Subject, Body: body}
	aps := apsFields{Alert: &alert, Sound: msg.Metadata["apns-sound"]}
	if s := msg.Metadata["apns-badge"]; s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			aps.Badge = &n
		}
	}
	if v := msg.Metadata["apns-thread-id"]; v != "" {
		aps.ThreadID = v
	}
	if msg.Metadata["apns-content-available"] == "1" {
		aps.ContentAvailable = 1
	}

	root := map[string]any{"aps": aps}
	// Copy any other metadata as top-level payload keys (custom data).
	for k, v := range msg.Metadata {
		if shouldSkipMetadataKey(k) {
			continue
		}
		root[k] = v
	}

	buf, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("notify/apns2: marshal: %w", err)
	}
	return buf, nil
}

// shouldSkipMetadataKey reports whether a Metadata key is handled via
// headers/aps fields (and therefore shouldn't be re-emitted as a
// custom top-level payload field).
func shouldSkipMetadataKey(k string) bool {
	switch k {
	case "apns-push-type", "apns-priority", "apns-id", "apns-collapse-id",
		"apns-sound", "apns-badge", "apns-thread-id", "apns-content-available":
		return true
	default:
		return false
	}
}

type apsFields struct {
	Alert            *apsAlert `json:"alert,omitempty"`
	Badge            *int      `json:"badge,omitempty"`
	Sound            string    `json:"sound,omitempty"`
	ThreadID         string    `json:"thread-id,omitempty"`
	ContentAvailable int       `json:"content-available,omitempty"`
}

type apsAlert struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}
