// Package ntfy sends notifications to an ntfy server (https://ntfy.sh or
// a self-hosted instance).
//
// Configure a topic and pass the server URL plus topic to [NewSender].
// For protected topics supply a bearer token or basic-auth credentials.
//
// Usage:
//
//	s, err := ntfy.NewSender(ntfy.Options{
//	    ServerURL: "https://ntfy.sh",
//	    Topic:     "my-alerts",
//	    Token:     os.Getenv("NTFY_TOKEN"),
//	})
//	notify.New(logger, notify.WithSender(s))
package ntfy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golusoris/golusoris/notify"
)

// Options configures the ntfy sender.
type Options struct {
	// ServerURL is the ntfy server base URL (e.g. "https://ntfy.sh"). Required.
	ServerURL string `koanf:"server_url"`
	// Topic is the ntfy topic to publish to. Required.
	Topic string `koanf:"topic"`
	// Priority is the default priority (1–5; "max"=5 … "min"=1). 0 leaves it unset.
	Priority int `koanf:"priority"`
	// Tags are default tags/emoji shortcodes attached to every message.
	Tags []string `koanf:"tags"`
	// Token is an optional bearer token for protected topics. Takes
	// precedence over Username/Password.
	Token string `koanf:"token"`
	// Username and Password are optional HTTP basic-auth credentials.
	Username string `koanf:"username"`
	Password string `koanf:"password"`
	// HTTPClient is optional; defaults to a 10s-timeout client.
	HTTPClient *http.Client
}

// Sender posts notify.Messages to an ntfy topic.
type Sender struct {
	opts Options
	hc   *http.Client
}

// NewSender returns an ntfy sender.
func NewSender(opts Options) (*Sender, error) {
	if opts.ServerURL == "" {
		return nil, errors.New("notify/ntfy: server_url is required")
	}
	if opts.Topic == "" {
		return nil, errors.New("notify/ntfy: topic is required")
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Sender{opts: opts, hc: hc}, nil
}

// Name implements [notify.Sender].
func (s *Sender) Name() string { return "ntfy" }

// Send implements [notify.Sender]. The message body is msg.Body, falling
// back to msg.Text; msg.Subject becomes the ntfy Title header. Priority and
// tags default from Options and may be overridden via msg.Metadata.
// msg.Metadata["click"] sets the Click header and msg.Metadata["icon"] sets
// the Icon header.
func (s *Sender) Send(ctx context.Context, msg notify.Message) error {
	message := msg.Body
	if message == "" {
		message = msg.Text
	}
	if message == "" {
		return errors.New("notify/ntfy: empty message")
	}

	target := strings.TrimRight(s.opts.ServerURL, "/") + "/" + strings.TrimLeft(s.opts.Topic, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(message))
	if err != nil {
		return fmt.Errorf("notify/ntfy: new request: %w", err)
	}
	s.setHeaders(req, msg)

	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("notify/ntfy: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("notify/ntfy: status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

// setHeaders applies the title, priority, tags, click, icon, and auth headers.
func (s *Sender) setHeaders(req *http.Request, msg notify.Message) {
	if msg.Subject != "" {
		req.Header.Set("Title", msg.Subject)
	}
	if p := s.priority(msg); p > 0 {
		req.Header.Set("Priority", strconv.Itoa(p))
	}
	if tags := s.tags(msg); tags != "" {
		req.Header.Set("Tags", tags)
	}
	if click := msg.Metadata["click"]; click != "" {
		req.Header.Set("Click", click)
	}
	if icon := msg.Metadata["icon"]; icon != "" {
		req.Header.Set("Icon", icon)
	}
	switch {
	case s.opts.Token != "":
		req.Header.Set("Authorization", "Bearer "+s.opts.Token)
	case s.opts.Username != "":
		req.SetBasicAuth(s.opts.Username, s.opts.Password)
	}
}

// priority resolves the priority: msg.Metadata["priority"] (if a valid
// integer) overrides Options.Priority.
func (s *Sender) priority(msg notify.Message) int {
	if raw, ok := msg.Metadata["priority"]; ok {
		var p int
		if _, err := fmt.Sscanf(raw, "%d", &p); err == nil {
			return p
		}
	}
	return s.opts.Priority
}

// tags resolves the tags: msg.Metadata["tags"] (comma-separated) overrides
// Options.Tags.
func (s *Sender) tags(msg notify.Message) string {
	if raw, ok := msg.Metadata["tags"]; ok && raw != "" {
		return raw
	}
	return strings.Join(s.opts.Tags, ",")
}
