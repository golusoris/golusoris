// Package out provides outbound webhook delivery with HMAC-SHA256 signing,
// exponential-backoff retry, dead-letter queue, and replay.
//
// Usage:
//
//	dispatcher := out.New(store, out.Options{}, logger, clk)
//
//	// Deliver to all active endpoints subscribed to "order.created":
//	err := dispatcher.Dispatch(ctx, "order.created", payload)
//
//	// Re-deliver a dead-lettered delivery:
//	err = dispatcher.Replay(ctx, deliveryID)
//
// The signing format is "sha256=<hex>" in [SignHeader] (default
// "X-Webhook-Signature"), matching [webhooks/in.HMAC].
package out

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/golusoris/golusoris/clock"
)

// Status is the delivery outcome.
type Status string

// Delivery status values.
const (
	StatusPending   Status = "pending"
	StatusDelivered Status = "delivered"
	StatusFailed    Status = "failed" // dead-lettered after all retries exhausted
)

// Endpoint is a registered webhook subscription.
type Endpoint struct {
	ID     string
	URL    string
	Secret string   // HMAC-SHA256 signing key
	Events []string // nil/empty = subscribe to all events
	Active bool
}

// Delivery records one outbound dispatch attempt.
type Delivery struct {
	ID         string
	EndpointID string
	Event      string
	Payload    []byte
	Attempts   int
	Status     Status
	StatusCode int // last HTTP response status code
	Error      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Store persists endpoints and delivery records. Callers supply their own
// implementation (e.g. Postgres, in-memory for tests).
type Store interface {
	SaveEndpoint(ctx context.Context, e Endpoint) error
	FindEndpoint(ctx context.Context, id string) (Endpoint, error)
	// ListEndpoints returns endpoints whose Events list contains event,
	// or all active endpoints when event is empty.
	ListEndpoints(ctx context.Context, event string) ([]Endpoint, error)
	DeleteEndpoint(ctx context.Context, id string) error

	SaveDelivery(ctx context.Context, d Delivery) error
	FindDelivery(ctx context.Context, id string) (Delivery, error)
	ListDeadLetters(ctx context.Context) ([]Delivery, error)
}

// Options tunes delivery behaviour.
type Options struct {
	// MaxAttempts is the total number of attempts before dead-lettering.
	// Default: 5.
	MaxAttempts int
	// Timeout is the per-request HTTP timeout. Default: 10s.
	Timeout time.Duration
	// Backoff returns how long to wait before attempt n (0-indexed).
	// Default: exponential 1s, 2s, 4s, 8s, …, capped at 5m.
	Backoff func(attempt int) time.Duration
	// SignHeader is the request header carrying "sha256=<hex>".
	// Default: "X-Webhook-Signature".
	SignHeader string
}

func (o *Options) defaults() {
	if o.MaxAttempts == 0 {
		o.MaxAttempts = 5
	}
	if o.Timeout == 0 {
		o.Timeout = 10 * time.Second
	}
	if o.Backoff == nil {
		o.Backoff = exponentialBackoff
	}
	if o.SignHeader == "" {
		o.SignHeader = "X-Webhook-Signature"
	}
}

func exponentialBackoff(attempt int) time.Duration {
	d := time.Second << attempt // 1s, 2s, 4s, 8s, …
	const maxBackoff = 5 * time.Minute
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

// Dispatcher signs and delivers outbound webhook payloads.
type Dispatcher struct {
	store  Store
	opts   Options
	client *http.Client
	clk    clock.Clock
	logger *slog.Logger
}

// New returns a Dispatcher. clk should be clock.NewFake() in tests.
func New(store Store, opts Options, logger *slog.Logger, clk clock.Clock) *Dispatcher {
	opts.defaults()
	return &Dispatcher{
		store:  store,
		opts:   opts,
		client: &http.Client{Timeout: opts.Timeout},
		clk:    clk,
		logger: logger,
	}
}

// Dispatch marshals payload as JSON and delivers it to every active endpoint
// subscribed to event. Each delivery runs synchronously; wrap in a goroutine
// or a job queue for background delivery.
func (d *Dispatcher) Dispatch(ctx context.Context, event string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webhooks/out: marshal payload: %w", err)
	}

	endpoints, err := d.store.ListEndpoints(ctx, event)
	if err != nil {
		return fmt.Errorf("webhooks/out: list endpoints: %w", err)
	}

	now := d.clk.Now()
	for _, ep := range endpoints {
		if !ep.Active {
			continue
		}
		del := Delivery{
			ID:         newID(),
			EndpointID: ep.ID,
			Event:      event,
			Payload:    body,
			Status:     StatusPending,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if saveErr := d.store.SaveDelivery(ctx, del); saveErr != nil {
			d.logger.WarnContext(ctx, "webhooks/out: save delivery", "err", saveErr, "endpoint", ep.ID)
			continue
		}
		if deliverErr := d.deliver(ctx, ep, &del); deliverErr != nil {
			d.logger.WarnContext(ctx, "webhooks/out: delivery failed", "endpoint", ep.ID, "delivery", del.ID, "err", deliverErr)
		}
	}
	return nil
}

// Replay re-delivers a dead-lettered delivery from scratch.
func (d *Dispatcher) Replay(ctx context.Context, deliveryID string) error {
	del, err := d.store.FindDelivery(ctx, deliveryID)
	if err != nil {
		return fmt.Errorf("webhooks/out: find delivery: %w", err)
	}
	ep, err := d.store.FindEndpoint(ctx, del.EndpointID)
	if err != nil {
		return fmt.Errorf("webhooks/out: find endpoint: %w", err)
	}
	del.Attempts = 0
	del.Status = StatusPending
	del.Error = ""
	return d.deliver(ctx, ep, &del)
}

// deliver attempts delivery up to MaxAttempts times with exponential backoff.
func (d *Dispatcher) deliver(ctx context.Context, ep Endpoint, del *Delivery) error {
	sig := sign([]byte(ep.Secret), del.Payload)

	for del.Attempts < d.opts.MaxAttempts {
		if del.Attempts > 0 {
			if wait := d.opts.Backoff(del.Attempts - 1); wait > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-d.clk.After(wait):
				}
			}
		}

		code, err := d.post(ctx, ep.URL, del.ID, del.Event, sig, del.Payload)
		del.Attempts++
		del.UpdatedAt = d.clk.Now()
		del.StatusCode = code

		if err == nil && code < 400 {
			del.Status = StatusDelivered
			del.Error = ""
			_ = d.store.SaveDelivery(ctx, *del)
			return nil
		}

		if err != nil {
			del.Error = err.Error()
		} else {
			del.Error = fmt.Sprintf("HTTP %d", code)
		}
		_ = d.store.SaveDelivery(ctx, *del)
	}

	del.Status = StatusFailed
	del.UpdatedAt = d.clk.Now()
	_ = d.store.SaveDelivery(ctx, *del)
	return fmt.Errorf("webhooks/out: delivery %s dead-lettered after %d attempts: %s", del.ID, del.Attempts, del.Error)
}

func (d *Dispatcher) post(ctx context.Context, url, deliveryID, event, sig string, body []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(d.opts.SignHeader, "sha256="+sig)
	req.Header.Set("X-Webhook-Event", event)
	req.Header.Set("X-Webhook-Delivery", deliveryID)

	resp, err := d.client.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// sign returns the hex-encoded HMAC-SHA256 of payload using secret.
func sign(secret, payload []byte) string {
	h := hmac.New(sha256.New, secret)
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// newID returns a 16-byte random hex string suitable for delivery IDs.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("webhooks/out: rand.Read: %v", err))
	}
	return hex.EncodeToString(b)
}
