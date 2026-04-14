// Package webpush sends browser push notifications via the Web Push
// Protocol (RFC 8030 / draft-ietf-webpush-encryption-08). It signs
// requests with VAPID (RFC 8292) so no Google/Mozilla API key is
// required.
//
// Usage:
//
//	// 1. Generate VAPID keys once (offline):
//	//      priv, pub, _ := webpush.NewVAPIDKeys()
//	//      → store priv securely; publish pub to browser subscribers.
//	// 2. Collect Subscription objects from client PushManager.subscribe().
//	// 3. Send:
//	s, _ := webpush.NewSender(webpush.Options{
//	    VAPIDPublicKey:  pub,
//	    VAPIDPrivateKey: priv,
//	    Subject:         "mailto:ops@example.com",
//	})
//	_ = s.Send(ctx, notify.Message{
//	    Body:     `{"title":"New message","body":"Hello"}`,
//	    Metadata: map[string]string{"subscription": encodedJSONSubscription},
//	})
//
// Message routing: [notify.Message.Metadata]["subscription"] carries
// the JSON-encoded subscription object produced by
// PushManager.subscribe(). This keeps the standard [notify.Message]
// shape while routing the payload to the right endpoint.
package webpush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	wp "github.com/SherClockHolmes/webpush-go"

	"github.com/golusoris/golusoris/notify"
)

// Options configures the Web Push sender.
type Options struct {
	// VAPIDPublicKey is a URL-safe-base64-encoded P-256 public key.
	// Required. Share with browser code that calls PushManager.subscribe.
	VAPIDPublicKey string `koanf:"vapid_public_key"`
	// VAPIDPrivateKey is a URL-safe-base64-encoded P-256 private key.
	// Required. Treat as a secret.
	VAPIDPrivateKey string `koanf:"vapid_private_key"`
	// Subject is the VAPID subject — an RFC 2368 mailto: URI or a URL
	// identifying the application server (e.g. "mailto:ops@example.com").
	// Required by some push services.
	Subject string `koanf:"subject"`
	// TTL is the max age (seconds) the push service may store the
	// message before delivery. Default 86400 (24h).
	TTL int `koanf:"ttl"`
	// Urgency signals delivery priority. Valid: "very-low", "low",
	// "normal" (default), "high".
	Urgency string `koanf:"urgency"`
	// Topic collapses older messages with the same topic. Optional.
	Topic string `koanf:"topic"`
	// HTTPClient is optional; defaults to a 10s-timeout client.
	HTTPClient *http.Client
}

// Sender sends notify.Messages to browser push subscriptions.
type Sender struct {
	opts Options
	hc   *http.Client
}

// NewSender returns a Web Push sender.
func NewSender(opts Options) (*Sender, error) {
	if opts.VAPIDPublicKey == "" {
		return nil, errors.New("notify/webpush: vapid_public_key is required")
	}
	if opts.VAPIDPrivateKey == "" {
		return nil, errors.New("notify/webpush: vapid_private_key is required")
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Sender{opts: opts, hc: hc}, nil
}

// Name implements [notify.Sender].
func (s *Sender) Name() string { return "webpush" }

// Send implements [notify.Sender]. The browser Subscription object must
// be JSON-encoded and placed in msg.Metadata["subscription"]. The push
// payload is msg.Body (use your own JSON / plain-text convention — the
// browser service worker sees it verbatim).
func (s *Sender) Send(ctx context.Context, msg notify.Message) error {
	subJSON := msg.Metadata["subscription"]
	if subJSON == "" {
		return errors.New("notify/webpush: Metadata[\"subscription\"] is required (JSON-encoded PushSubscription)")
	}
	var sub wp.Subscription
	if err := json.Unmarshal([]byte(subJSON), &sub); err != nil {
		return fmt.Errorf("notify/webpush: subscription: %w", err)
	}
	if msg.Body == "" {
		return errors.New("notify/webpush: msg.Body required (payload sent verbatim to the service worker)")
	}

	ttl := s.opts.TTL
	if ttl == 0 {
		ttl = 86400
	}
	resp, err := wp.SendNotificationWithContext(ctx, []byte(msg.Body), &sub, &wp.Options{
		HTTPClient:      s.hc,
		Subscriber:      s.opts.Subject,
		VAPIDPublicKey:  s.opts.VAPIDPublicKey,
		VAPIDPrivateKey: s.opts.VAPIDPrivateKey,
		TTL:             ttl,
		Urgency:         wp.Urgency(s.opts.Urgency),
		Topic:           s.opts.Topic,
	})
	if err != nil {
		return fmt.Errorf("notify/webpush: send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("notify/webpush: status %d", resp.StatusCode)
	}
	return nil
}

// NewVAPIDKeys generates a fresh VAPID keypair (URL-safe-base64 encoded
// P-256 keys). Store the private key as a secret; publish the public
// key to clients that call PushManager.subscribe(). Keys are stable
// per-origin — generate once and keep.
func NewVAPIDKeys() (privateKey, publicKey string, err error) {
	priv, pub, err := wp.GenerateVAPIDKeys()
	if err != nil {
		return "", "", fmt.Errorf("notify/webpush: generate: %w", err)
	}
	return priv, pub, nil
}

// EncodeSubscription is a convenience for apps that want to stash a
// JSON-encoded Subscription into [notify.Message.Metadata].
func EncodeSubscription(endpoint, p256dh, auth string) (string, error) {
	sub := wp.Subscription{
		Endpoint: endpoint,
		Keys: wp.Keys{
			P256dh: p256dh,
			Auth:   auth,
		},
	}
	b, err := json.Marshal(sub)
	if err != nil {
		return "", fmt.Errorf("notify/webpush: encode: %w", err)
	}
	return string(b), nil
}
