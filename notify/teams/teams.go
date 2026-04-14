// Package teams sends notifications to Microsoft Teams via legacy
// incoming webhooks (Connectors) or Power Automate / Workflow URLs.
//
// The wire format is a MessageCard — simple JSON with title/text +
// optional theme color. Teams accepts Adaptive Cards too, but the
// MessageCard shape stays stable across both classic connectors and
// Workflow endpoints.
//
// Usage:
//
//	s, err := teams.NewSender(teams.Options{WebhookURL: url})
//	notify.New(logger, notify.WithSender(s))
package teams

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/golusoris/golusoris/notify"
)

// Options configures the Teams sender.
type Options struct {
	// WebhookURL is the full Teams incoming-webhook URL. Required.
	WebhookURL string `koanf:"webhook_url"`
	// ThemeColor is a hex colour (without '#') applied to the MessageCard
	// stripe — e.g. "0076D7". Optional.
	ThemeColor string `koanf:"theme_color"`
	// HTTPClient is optional; defaults to a 10s-timeout client.
	HTTPClient *http.Client
}

// Sender posts notify.Messages to a Teams incoming webhook.
type Sender struct {
	opts Options
	hc   *http.Client
}

// NewSender returns a Teams sender.
func NewSender(opts Options) (*Sender, error) {
	if opts.WebhookURL == "" {
		return nil, errors.New("notify/teams: webhook_url is required")
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Sender{opts: opts, hc: hc}, nil
}

// Name implements [notify.Sender].
func (s *Sender) Name() string { return "teams" }

// Send implements [notify.Sender]. Uses msg.Subject as the MessageCard
// title and msg.Body (or msg.Text, or stripped HTML) as the body.
func (s *Sender) Send(ctx context.Context, msg notify.Message) error {
	text := msg.Body
	if text == "" {
		text = msg.Text
	}
	if text == "" {
		text = msg.HTML
	}
	if text == "" && msg.Subject == "" {
		return errors.New("notify/teams: subject or body required")
	}

	card := messageCard{
		Schema:     "http://schema.org/extensions",
		Type:       "MessageCard",
		ThemeColor: s.opts.ThemeColor,
		Summary:    msg.Subject,
		Title:      msg.Subject,
		Text:       text,
	}
	if card.Summary == "" {
		card.Summary = firstLine(text)
	}
	body, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("notify/teams: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.opts.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify/teams: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("notify/teams: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("notify/teams: status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i > 0 {
		return s[:i]
	}
	return s
}

// messageCard is the Teams legacy connector wire format.
// Tagliatelle snake-case rule is exempted for this package because the
// Teams MessageCard schema uses @type / @context PascalCase-ish keys.
type messageCard struct {
	Schema     string `json:"@context"`
	Type       string `json:"@type"`
	ThemeColor string `json:"themeColor,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Title      string `json:"title,omitempty"`
	Text       string `json:"text,omitempty"`
}
