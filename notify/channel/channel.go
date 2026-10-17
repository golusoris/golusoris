// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from SnatchArr api/internal/notify/sender.go at dccf1971.

// Package channel provides generic notification channel dispatch and secret redaction.
//
// It routes notifications across chat providers (Discord, Slack, Telegram,
// ntfy, Gotify, Teams) and outgoing webhooks with secret scrubbing and retries.
package channel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/discord"
	"github.com/golusoris/golusoris/notify/gotify"
	"github.com/golusoris/golusoris/notify/ntfy"
	"github.com/golusoris/golusoris/notify/slack"
	"github.com/golusoris/golusoris/notify/teams"
	"github.com/golusoris/golusoris/notify/telegram"
	"github.com/golusoris/golusoris/webhooks/out"
)

// Kind identifies the notification delivery channel.
type Kind string

// Channel kinds.
const (
	Discord  Kind = "discord"
	Slack    Kind = "slack"
	Telegram Kind = "telegram"
	Ntfy     Kind = "ntfy"
	Gotify   Kind = "gotify"
	Teams    Kind = "teams"
	Webhook  Kind = "webhook"
)

// Valid reports whether the channel kind is recognized.
func (k Kind) Valid() bool {
	switch k {
	case Discord, Slack, Telegram, Ntfy, Gotify, Teams, Webhook:
		return true
	default:
		return false
	}
}

// Config holds the configuration options across all channel kinds.
type Config struct {
	// Secret fields (never returned in responses; encrypted at rest)
	WebhookURL string `json:"webhook_url,omitempty"`
	Token      string `json:"token,omitempty"`
	BotToken   string `json:"bot_token,omitempty"`
	AppToken   string `json:"app_token,omitempty"`
	Password   string `json:"password,omitempty"`
	Secret     string `json:"secret,omitempty"`

	// Public fields
	ServerURL           string   `json:"server_url,omitempty"`
	Topic               string   `json:"topic,omitempty"`
	ChatID              string   `json:"chat_id,omitempty"`
	Channel             string   `json:"channel,omitempty"`
	Username            string   `json:"username,omitempty"`
	AvatarURL           string   `json:"avatar_url,omitempty"`
	IconEmoji           string   `json:"icon_emoji,omitempty"`
	ParseMode           string   `json:"parse_mode,omitempty"`
	ThemeColor          string   `json:"theme_color,omitempty"`
	Priority            int      `json:"priority,omitempty"`
	Tags                []string `json:"tags,omitempty"`
	URL                 string   `json:"url,omitempty"`
	AllowPrivateNetwork bool     `json:"allow_private_network,omitempty"`
	AllowInsecureHTTP   bool     `json:"allow_insecure_http,omitempty"`
}

// HasSecret reports whether any secret field is populated.
func (c Config) HasSecret() bool {
	return c.WebhookURL != "" || c.Token != "" || c.BotToken != "" ||
		c.AppToken != "" || c.Password != "" || c.Secret != ""
}

// Redacted returns a copy of the config with all secret fields cleared.
func (c Config) Redacted() Config {
	cp := c
	cp.WebhookURL = ""
	cp.Token = ""
	cp.BotToken = ""
	cp.AppToken = ""
	cp.Password = ""
	cp.Secret = ""
	return cp
}

// MergeSecrets returns a copy of c, taking any missing secret field from existing.
func (c Config) MergeSecrets(existing Config) Config {
	cp := c
	if cp.WebhookURL == "" {
		cp.WebhookURL = existing.WebhookURL
	}
	if cp.Token == "" {
		cp.Token = existing.Token
	}
	if cp.BotToken == "" {
		cp.BotToken = existing.BotToken
	}
	if cp.AppToken == "" {
		cp.AppToken = existing.AppToken
	}
	if cp.Password == "" {
		cp.Password = existing.Password
	}
	if cp.Secret == "" {
		cp.Secret = existing.Secret
	}
	return cp
}

// ErrUnsupportedKind is returned when the channel kind is not recognized.
var ErrUnsupportedKind = errors.New("notify/channel: unsupported channel kind")

// ErrWebhookNotChatSender is returned when attempting to build a chat sender for a webhook channel.
var ErrWebhookNotChatSender = errors.New("notify/channel: webhook channel is not a chat sender")

// Sender constructs a notify.Sender for the given chat channel kind and configuration.
func Sender(kind Kind, cfg Config) (notify.Sender, error) {
	switch kind {
	case Discord:
		return buildDiscord(cfg)
	case Slack:
		return buildSlack(cfg)
	case Telegram:
		return buildTelegram(cfg)
	case Ntfy:
		return buildNtfy(cfg)
	case Gotify:
		return buildGotify(cfg)
	case Teams:
		return buildTeams(cfg)
	case Webhook:
		return nil, fmt.Errorf("notify/channel: build sender: %w", ErrWebhookNotChatSender)
	default:
		return nil, fmt.Errorf("notify/channel: build sender: %w: %s", ErrUnsupportedKind, kind)
	}
}

func buildDiscord(cfg Config) (notify.Sender, error) {
	s, err := discord.NewSender(discord.Options{
		WebhookURL: cfg.WebhookURL,
		Username:   cfg.Username,
		AvatarURL:  cfg.AvatarURL,
	})
	if err != nil {
		return nil, fmt.Errorf("notify/channel: build discord sender: %w", err)
	}
	return s, nil
}

func buildSlack(cfg Config) (notify.Sender, error) {
	s, err := slack.NewSender(slack.Options{
		WebhookURL: cfg.WebhookURL,
		Channel:    cfg.Channel,
		Username:   cfg.Username,
		IconEmoji:  cfg.IconEmoji,
	})
	if err != nil {
		return nil, fmt.Errorf("notify/channel: build slack sender: %w", err)
	}
	return s, nil
}

func buildTelegram(cfg Config) (notify.Sender, error) {
	s, err := telegram.NewSender(telegram.Options{
		BotToken:  cfg.BotToken,
		ChatID:    cfg.ChatID,
		ParseMode: telegram.ParseMode(cfg.ParseMode),
	})
	if err != nil {
		return nil, fmt.Errorf("notify/channel: build telegram sender: %w", err)
	}
	return s, nil
}

func buildNtfy(cfg Config) (notify.Sender, error) {
	s, err := ntfy.NewSender(ntfy.Options{
		ServerURL: cfg.ServerURL,
		Topic:     cfg.Topic,
		Token:     cfg.Token,
		Priority:  cfg.Priority,
		Tags:      cfg.Tags,
	})
	if err != nil {
		return nil, fmt.Errorf("notify/channel: build ntfy sender: %w", err)
	}
	return s, nil
}

func buildGotify(cfg Config) (notify.Sender, error) {
	s, err := gotify.NewSender(gotify.Options{
		ServerURL: cfg.ServerURL,
		AppToken:  cfg.AppToken,
		Priority:  cfg.Priority,
	})
	if err != nil {
		return nil, fmt.Errorf("notify/channel: build gotify sender: %w", err)
	}
	return s, nil
}

func buildTeams(cfg Config) (notify.Sender, error) {
	s, err := teams.NewSender(teams.Options{
		WebhookURL: cfg.WebhookURL,
		ThemeColor: cfg.ThemeColor,
	})
	if err != nil {
		return nil, fmt.Errorf("notify/channel: build teams sender: %w", err)
	}
	return s, nil
}

// ErrNoEvent is returned when a webhook delivery names no event.
var ErrNoEvent = errors.New("notify/channel: webhook event is empty")

// WebhookOptions configures bounds and retry behavior for webhook delivery.
type WebhookOptions struct {
	MaxAttempts int
	Timeout     time.Duration
	Backoff     func(attempt int) time.Duration
}

// WebhookRequest holds caller-supplied parameters for webhook delivery.
type WebhookRequest struct {
	EndpointID string
	Event      string
	Payload    any
	Store      out.Store
	Logger     *slog.Logger
	Clock      clock.Clock
	Options    WebhookOptions
}

type singleEndpointStore struct {
	store    out.Store
	endpoint out.Endpoint
}

func (s *singleEndpointStore) ListEndpoints(_ context.Context, _ string) ([]out.Endpoint, error) {
	return []out.Endpoint{s.endpoint}, nil
}

func (s *singleEndpointStore) SaveEndpoint(ctx context.Context, e out.Endpoint) error {
	if s.store != nil {
		if err := s.store.SaveEndpoint(ctx, e); err != nil {
			return fmt.Errorf("notify/channel: save endpoint: %w", err)
		}
	}
	return nil
}

func (s *singleEndpointStore) FindEndpoint(ctx context.Context, id string) (out.Endpoint, error) {
	if id == s.endpoint.ID {
		return s.endpoint, nil
	}
	if s.store != nil {
		ep, err := s.store.FindEndpoint(ctx, id)
		if err != nil {
			return out.Endpoint{}, fmt.Errorf("notify/channel: find endpoint: %w", err)
		}
		return ep, nil
	}
	return out.Endpoint{}, errors.New("notify/channel: endpoint not found")
}

func (s *singleEndpointStore) DeleteEndpoint(ctx context.Context, id string) error {
	if s.store != nil {
		if err := s.store.DeleteEndpoint(ctx, id); err != nil {
			return fmt.Errorf("notify/channel: delete endpoint: %w", err)
		}
	}
	return nil
}

func (s *singleEndpointStore) SaveDelivery(ctx context.Context, d out.Delivery) error {
	if s.store != nil {
		if err := s.store.SaveDelivery(ctx, d); err != nil {
			return fmt.Errorf("notify/channel: save delivery: %w", err)
		}
	}
	return nil
}

func (s *singleEndpointStore) FindDelivery(ctx context.Context, id string) (out.Delivery, error) {
	if s.store != nil {
		del, err := s.store.FindDelivery(ctx, id)
		if err != nil {
			return out.Delivery{}, fmt.Errorf("notify/channel: find delivery: %w", err)
		}
		return del, nil
	}
	return out.Delivery{}, errors.New("notify/channel: delivery not found")
}

func (s *singleEndpointStore) ListDeadLetters(ctx context.Context) ([]out.Delivery, error) {
	if s.store != nil {
		dels, err := s.store.ListDeadLetters(ctx)
		if err != nil {
			return nil, fmt.Errorf("notify/channel: list dead letters: %w", err)
		}
		return dels, nil
	}
	return nil, nil
}

// DeliverWebhook delivers an event payload to a single webhook endpoint.
func DeliverWebhook(ctx context.Context, cfg Config, req WebhookRequest) error {
	if req.Event == "" {
		return ErrNoEvent
	}
	epID := req.EndpointID
	if epID == "" {
		epID = "default"
	}
	ep := out.Endpoint{
		ID:     epID,
		URL:    cfg.URL,
		Secret: cfg.Secret,
		Active: true,
	}
	opts := out.Options{
		AllowPrivateNetwork: cfg.AllowPrivateNetwork,
		AllowInsecureHTTP:   cfg.AllowInsecureHTTP,
		MaxAttempts:         req.Options.MaxAttempts,
		Timeout:             req.Options.Timeout,
		Backoff:             req.Options.Backoff,
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 3
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.Backoff == nil {
		opts.Backoff = func(int) time.Duration { return 50 * time.Millisecond }
	}
	store := &singleEndpointStore{store: req.Store, endpoint: ep}
	d := out.New(store, opts, req.Logger, req.Clock)
	if err := d.Dispatch(ctx, req.Event, req.Payload); err != nil {
		return fmt.Errorf("notify/channel: deliver webhook: %w", err)
	}
	return nil
}

func prepareMessage(msg notify.Message) notify.Message {
	switch {
	case msg.Body == "" && msg.Text == "":
		msg.Body = msg.Subject
		msg.Text = msg.Subject
	case msg.Body == "":
		msg.Body = msg.Text
	case msg.Text == "":
		msg.Text = msg.Body
	}
	return msg
}

func deliverChat(ctx context.Context, kind Kind, cfg Config, msg notify.Message) error {
	s, err := Sender(kind, cfg)
	if err != nil {
		return fmt.Errorf("notify/channel: deliver: %w", err)
	}
	if err := s.Send(ctx, prepareMessage(msg)); err != nil {
		return fmt.Errorf("notify/channel: deliver: %w", err)
	}
	return nil
}

// Deliver routes msg to the channel of kind. Chat kinds ignore hook; the
// Webhook kind dispatches hook, with msg as the payload when hook has none.
func Deliver(ctx context.Context, kind Kind, cfg Config, msg notify.Message, hook WebhookRequest) error {
	if kind == Webhook {
		if hook.Payload == nil {
			hook.Payload = msg
		}
		return DeliverWebhook(ctx, cfg, hook)
	}
	return deliverChat(ctx, kind, cfg, msg)
}

func urlUserInfoSecrets(rawURL string) []string {
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return nil
	}
	var res []string
	if pass, ok := u.User.Password(); ok && pass != "" {
		res = append(res, pass)
	}
	if uInfo := u.User.String(); uInfo != "" {
		res = append(res, uInfo)
	}
	return res
}

// Redact scrubs secrets from error messages before logging or storing events.
func Redact(err error, cfg Config) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	secrets := []string{
		cfg.WebhookURL, cfg.URL, cfg.Token, cfg.BotToken, cfg.AppToken, cfg.Password, cfg.Secret,
	}
	for _, rawURL := range []string{cfg.ServerURL, cfg.WebhookURL, cfg.URL} {
		secrets = append(secrets, urlUserInfoSecrets(rawURL)...)
	}
	for _, s := range secrets {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "[REDACTED]")
		}
	}
	return msg
}
