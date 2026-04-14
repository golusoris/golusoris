// Package discord sends notifications to Discord via incoming webhooks.
//
// Configure a Discord channel webhook (Server Settings → Integrations →
// Webhooks → New Webhook) and pass the resulting URL to [NewSender].
package discord

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

// Options configures the Discord sender.
type Options struct {
	// WebhookURL is the full webhook URL from Discord.
	WebhookURL string `koanf:"webhook_url"`
	// Username overrides the webhook's default display name.
	Username string `koanf:"username"`
	// AvatarURL overrides the webhook's default avatar.
	AvatarURL string `koanf:"avatar_url"`
	// HTTPClient is the optional HTTP client; defaults to a 10s-timeout client.
	HTTPClient *http.Client
}

// Sender posts notify.Message bodies to a Discord webhook.
type Sender struct {
	opts Options
	hc   *http.Client
}

// NewSender returns a Discord sender.
func NewSender(opts Options) (*Sender, error) {
	if opts.WebhookURL == "" {
		return nil, errors.New("notify/discord: webhook_url is required")
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Sender{opts: opts, hc: hc}, nil
}

// Name implements [notify.Sender].
func (s *Sender) Name() string { return "discord" }

// Send implements [notify.Sender]. The Body field is sent as the message
// content; if Body is empty, a Subject + Text fallback is used.
func (s *Sender) Send(ctx context.Context, msg notify.Message) error {
	content := msg.Body
	if content == "" {
		var b strings.Builder
		if msg.Subject != "" {
			b.WriteString("**")
			b.WriteString(msg.Subject)
			b.WriteString("**\n")
		}
		b.WriteString(msg.Text)
		content = b.String()
	}
	if content == "" {
		return errors.New("notify/discord: empty message")
	}
	payload := struct {
		Content   string `json:"content"`
		Username  string `json:"username,omitempty"`
		AvatarURL string `json:"avatar_url,omitempty"`
	}{Content: content, Username: s.opts.Username, AvatarURL: s.opts.AvatarURL}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify/discord: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.opts.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify/discord: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("notify/discord: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("notify/discord: status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}
