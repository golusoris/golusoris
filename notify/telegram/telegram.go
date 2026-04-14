// Package telegram sends chat notifications via the Telegram Bot API.
//
// Intended for ops/alert channels — not for user-facing email. Configure
// a bot via [@BotFather] and place it in the target chat.
//
// Usage:
//
//	s, err := telegram.NewSender(telegram.Options{
//	    BotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
//	    ChatID:   "-1001234567890",
//	})
//	notify.New(logger, notify.WithSender(s))
//
// [@BotFather]: https://t.me/BotFather
package telegram

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

// DefaultEndpoint is the Telegram Bot API base.
const DefaultEndpoint = "https://api.telegram.org"

// ParseMode controls Telegram's message formatting.
type ParseMode string

// Parse-mode values understood by Telegram.
const (
	ParseModeNone       ParseMode = ""
	ParseModeHTML       ParseMode = "HTML"
	ParseModeMarkdownV2 ParseMode = "MarkdownV2"
)

// Options configures the Telegram sender.
type Options struct {
	// BotToken is the bot auth token from @BotFather (e.g. "123:ABC…"). Required.
	BotToken string `koanf:"bot_token"`
	// ChatID is the default chat identifier — either a numeric ID (can
	// be negative for groups/channels) or a public channel username
	// prefixed with "@". Optional — msg.To[0] overrides per-send.
	ChatID string `koanf:"chat_id"`
	// ParseMode is the default body parse mode ("HTML", "MarkdownV2", or "").
	ParseMode ParseMode `koanf:"parse_mode"`
	// DisableWebPagePreview suppresses link previews by default.
	DisableWebPagePreview bool `koanf:"disable_web_page_preview"`
	// DisableNotification sends the message silently by default.
	DisableNotification bool `koanf:"disable_notification"`
	// Endpoint overrides the API base (tests).
	Endpoint string `koanf:"endpoint"`
	// HTTPClient is optional; defaults to a 10s-timeout client.
	HTTPClient *http.Client
}

// Sender posts notify.Messages to Telegram's sendMessage endpoint.
type Sender struct {
	opts     Options
	endpoint string
	hc       *http.Client
}

// NewSender returns a Telegram sender.
func NewSender(opts Options) (*Sender, error) {
	if opts.BotToken == "" {
		return nil, errors.New("notify/telegram: bot_token is required")
	}
	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Sender{opts: opts, endpoint: endpoint, hc: hc}, nil
}

// Name implements [notify.Sender].
func (s *Sender) Name() string { return "telegram" }

// Send implements [notify.Sender]. The chat is resolved in priority
// order: msg.To[0] → opts.ChatID. Body text is msg.Body, falling back
// to Subject+"\n"+Text.
func (s *Sender) Send(ctx context.Context, msg notify.Message) error {
	chat := s.opts.ChatID
	if len(msg.To) > 0 && msg.To[0] != "" {
		chat = msg.To[0]
	}
	if chat == "" {
		return errors.New("notify/telegram: chat_id is required (msg.To[0] or Options.ChatID)")
	}

	text := msg.Body
	if text == "" {
		var b strings.Builder
		if msg.Subject != "" {
			b.WriteString(msg.Subject)
		}
		if msg.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(msg.Text)
		}
		text = b.String()
	}
	if text == "" {
		return errors.New("notify/telegram: body required (Message.Body or Subject/Text)")
	}

	payload := telegramPayload{
		ChatID:                chat,
		Text:                  text,
		ParseMode:             string(s.opts.ParseMode),
		DisableWebPagePreview: s.opts.DisableWebPagePreview,
		DisableNotification:   s.opts.DisableNotification,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify/telegram: marshal: %w", err)
	}

	target := strings.TrimRight(s.endpoint, "/") + "/bot" + s.opts.BotToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify/telegram: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("notify/telegram: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("notify/telegram: status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

type telegramPayload struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	ParseMode             string `json:"parse_mode,omitempty"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview,omitempty"`
	DisableNotification   bool   `json:"disable_notification,omitempty"`
}
