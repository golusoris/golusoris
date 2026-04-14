// Package mailgun sends transactional email via Mailgun's HTTP API.
//
// Raw HTTP — we don't pull in mailgun-go/v4 because its transitive deps
// are heavy and the wire format we need is small.
//
// Usage:
//
//	s, err := mailgun.NewSender(mailgun.Options{
//	    Domain: "mg.example.com",
//	    APIKey: os.Getenv("MAILGUN_API_KEY"),
//	    From:   "noreply@example.com",
//	})
//	notify.New(logger, notify.WithSender(s))
package mailgun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golusoris/golusoris/notify"
)

// DefaultEndpoint is Mailgun's US region REST API base.
const DefaultEndpoint = "https://api.mailgun.net/v3"

// EURegionEndpoint is the Mailgun EU region base.
const EURegionEndpoint = "https://api.eu.mailgun.net/v3"

// Options configures the Mailgun sender.
type Options struct {
	// Domain is the Mailgun sending domain (e.g. "mg.example.com"). Required.
	Domain string `koanf:"domain"`
	// APIKey is a Mailgun private API key. Required.
	APIKey string `koanf:"api_key"`
	// From is the default sender (RFC 5322 mailbox). Required.
	From string `koanf:"from"`
	// ReplyTo overrides the default reply-to header.
	ReplyTo string `koanf:"reply_to"`
	// Endpoint overrides the API base — default is [DefaultEndpoint]
	// (US); set [EURegionEndpoint] for EU-hosted accounts or an
	// httptest URL for tests.
	Endpoint string `koanf:"endpoint"`
	// HTTPClient is optional; defaults to a 10s-timeout client.
	HTTPClient *http.Client
}

// Sender posts notify.Messages to Mailgun's /messages endpoint.
type Sender struct {
	opts     Options
	endpoint string
	hc       *http.Client
}

// NewSender returns a Mailgun sender. Returns an error if Domain, APIKey
// or From is empty.
func NewSender(opts Options) (*Sender, error) {
	if opts.Domain == "" {
		return nil, errors.New("notify/mailgun: domain is required")
	}
	if opts.APIKey == "" {
		return nil, errors.New("notify/mailgun: api_key is required")
	}
	if opts.From == "" {
		return nil, errors.New("notify/mailgun: from is required")
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
func (s *Sender) Name() string { return "mailgun" }

// Send implements [notify.Sender]. Maps notify.Message to a Mailgun
// form-encoded POST. Requires HTML or Text.
func (s *Sender) Send(ctx context.Context, msg notify.Message) error {
	if len(msg.To) == 0 {
		return errors.New("notify/mailgun: at least one recipient required")
	}
	if msg.HTML == "" && msg.Text == "" {
		return errors.New("notify/mailgun: html or text body required")
	}
	from := msg.From
	if from == "" {
		from = s.opts.From
	}

	form := url.Values{}
	form.Set("from", from)
	for _, to := range msg.To {
		form.Add("to", to)
	}
	for _, cc := range msg.CC {
		form.Add("cc", cc)
	}
	for _, bcc := range msg.BCC {
		form.Add("bcc", bcc)
	}
	form.Set("subject", msg.Subject)
	if msg.HTML != "" {
		form.Set("html", msg.HTML)
	}
	if msg.Text != "" {
		form.Set("text", msg.Text)
	}
	if s.opts.ReplyTo != "" {
		form.Set("h:Reply-To", s.opts.ReplyTo)
	}
	// Mailgun stores Metadata keys as v:<key> user-variables, queryable
	// in their event logs.
	for k, v := range msg.Metadata {
		form.Set("v:"+k, v)
	}

	target := strings.TrimRight(s.endpoint, "/") + "/" + s.opts.Domain + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return fmt.Errorf("notify/mailgun: new request: %w", err)
	}
	req.SetBasicAuth("api", s.opts.APIKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("notify/mailgun: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("notify/mailgun: status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}
