// Package slack sends notifications to Slack via incoming webhooks.
//
// Configure a Slack incoming webhook (https://api.slack.com/messaging/webhooks)
// and pass the resulting URL to [NewSender].
package slack

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

// Options configures the Slack sender.
type Options struct {
	WebhookURL string `koanf:"webhook_url"`
	// Channel overrides the webhook's default channel (e.g. "#alerts").
	Channel string `koanf:"channel"`
	// Username overrides the webhook's default display name.
	Username string `koanf:"username"`
	// IconEmoji is the optional bot icon (e.g. ":robot_face:").
	IconEmoji  string `koanf:"icon_emoji"`
	HTTPClient *http.Client
}

// Sender posts notify.Message bodies to a Slack incoming webhook.
type Sender struct {
	opts Options
	hc   *http.Client
}

// NewSender returns a Slack sender.
func NewSender(opts Options) (*Sender, error) {
	if opts.WebhookURL == "" {
		return nil, errors.New("notify/slack: webhook_url is required")
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Sender{opts: opts, hc: hc}, nil
}

// Name implements [notify.Sender].
func (s *Sender) Name() string { return "slack" }

// Send implements [notify.Sender]. Body is the Slack `text`; if empty,
// Subject + Text are concatenated.
func (s *Sender) Send(ctx context.Context, msg notify.Message) error {
	text := msg.Body
	if text == "" {
		var b strings.Builder
		if msg.Subject != "" {
			b.WriteString("*")
			b.WriteString(msg.Subject)
			b.WriteString("*\n")
		}
		b.WriteString(msg.Text)
		text = b.String()
	}
	if text == "" {
		return errors.New("notify/slack: empty message")
	}
	payload := struct {
		Text      string `json:"text"`
		Channel   string `json:"channel,omitempty"`
		Username  string `json:"username,omitempty"`
		IconEmoji string `json:"icon_emoji,omitempty"`
	}{Text: text, Channel: s.opts.Channel, Username: s.opts.Username, IconEmoji: s.opts.IconEmoji}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify/slack: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.opts.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify/slack: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("notify/slack: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("notify/slack: status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}
