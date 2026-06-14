// Package gotify sends notifications to a self-hosted Gotify server.
//
// Configure a Gotify application (Gotify UI → Apps → Create Application)
// and pass the server URL plus the application token to [NewSender].
//
// Usage:
//
//	s, err := gotify.NewSender(gotify.Options{
//	    ServerURL: "https://gotify.example.com",
//	    AppToken:  os.Getenv("GOTIFY_APP_TOKEN"),
//	})
//	notify.New(logger, notify.WithSender(s))
package gotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golusoris/golusoris/notify"
)

// Options configures the Gotify sender.
type Options struct {
	// ServerURL is the Gotify server base URL (e.g. "https://gotify.example.com"). Required.
	ServerURL string `koanf:"server_url"`
	// AppToken is the application token from the Gotify UI. Required.
	AppToken string `koanf:"app_token"`
	// Priority is the default message priority (0–10; higher is louder).
	Priority int `koanf:"priority"`
	// HTTPClient is optional; defaults to a 10s-timeout client.
	HTTPClient *http.Client
}

// Sender posts notify.Messages to a Gotify server's /message endpoint.
type Sender struct {
	opts Options
	hc   *http.Client
}

// NewSender returns a Gotify sender.
func NewSender(opts Options) (*Sender, error) {
	if opts.ServerURL == "" {
		return nil, errors.New("notify/gotify: server_url is required")
	}
	if opts.AppToken == "" {
		return nil, errors.New("notify/gotify: app_token is required")
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Sender{opts: opts, hc: hc}, nil
}

// Name implements [notify.Sender].
func (s *Sender) Name() string { return "gotify" }

// Send implements [notify.Sender]. The message body is msg.Body, falling
// back to msg.Text; msg.Subject becomes the Gotify title. The priority is
// taken from msg.Metadata["priority"] if a valid integer, else Options.Priority.
func (s *Sender) Send(ctx context.Context, msg notify.Message) error {
	message := msg.Body
	if message == "" {
		message = msg.Text
	}
	if message == "" {
		return errors.New("notify/gotify: empty message")
	}

	payload := gotifyPayload{
		Title:    msg.Subject,
		Message:  message,
		Priority: s.priority(msg),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify/gotify: marshal: %w", err)
	}

	target := strings.TrimRight(s.opts.ServerURL, "/") + "/message?token=" + url.QueryEscape(s.opts.AppToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify/gotify: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("notify/gotify: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("notify/gotify: status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

// priority resolves the message priority: msg.Metadata["priority"] (if a
// valid integer) overrides Options.Priority.
func (s *Sender) priority(msg notify.Message) int {
	if raw, ok := msg.Metadata["priority"]; ok {
		var p int
		if _, err := fmt.Sscanf(raw, "%d", &p); err == nil {
			return p
		}
	}
	return s.opts.Priority
}

type gotifyPayload struct {
	Title    string `json:"title,omitempty"`
	Message  string `json:"message"`
	Priority int    `json:"priority"`
}
