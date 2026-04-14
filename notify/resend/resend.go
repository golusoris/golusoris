// Package resend sends transactional email via Resend's HTTP API.
//
// The Resend wire format is small enough that we use raw HTTP instead
// of pulling in resend-go/v2; this keeps the dep graph lean and the
// surface auditable.
//
// Usage:
//
//	s, err := resend.NewSender(resend.Options{
//	    APIKey: os.Getenv("RESEND_API_KEY"),
//	    From:   "noreply@example.com",
//	})
//	notify.New(logger, notify.WithSender(s))
package resend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/golusoris/golusoris/notify"
)

// DefaultEndpoint is Resend's REST API base.
const DefaultEndpoint = "https://api.resend.com/emails"

// Options configures the Resend sender.
type Options struct {
	// APIKey is a Resend API key (re_…). Required.
	APIKey string `koanf:"api_key"`
	// From is the default sender address — required by Resend
	// (RFC 5322 mailbox; e.g. "Example <noreply@example.com>").
	From string `koanf:"from"`
	// ReplyTo overrides the default reply-to header.
	ReplyTo string `koanf:"reply_to"`
	// Endpoint overrides the API base — useful for tests / EU region
	// (https://api.resend.com is the default; EU customers may set
	// https://api.eu.resend.com).
	Endpoint string `koanf:"endpoint"`
	// HTTPClient is the optional HTTP client; defaults to a 10s-timeout
	// client.
	HTTPClient *http.Client
}

// Sender posts notify.Messages to Resend's /emails endpoint.
type Sender struct {
	opts     Options
	endpoint string
	hc       *http.Client
}

// NewSender returns a Resend sender. Returns an error if APIKey or From
// is empty.
func NewSender(opts Options) (*Sender, error) {
	if opts.APIKey == "" {
		return nil, errors.New("notify/resend: api_key is required")
	}
	if opts.From == "" {
		return nil, errors.New("notify/resend: from is required")
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
func (s *Sender) Name() string { return "resend" }

// Send implements [notify.Sender]. Maps notify.Message to the Resend
// /emails request body. Resend requires HTML or Text — the call returns
// an error if both are empty.
func (s *Sender) Send(ctx context.Context, msg notify.Message) error {
	if len(msg.To) == 0 {
		return errors.New("notify/resend: at least one recipient required")
	}
	if msg.HTML == "" && msg.Text == "" {
		return errors.New("notify/resend: html or text body required")
	}
	from := msg.From
	if from == "" {
		from = s.opts.From
	}
	payload := resendPayload{
		From:    from,
		To:      msg.To,
		CC:      msg.CC,
		BCC:     msg.BCC,
		Subject: msg.Subject,
		HTML:    msg.HTML,
		Text:    msg.Text,
		ReplyTo: s.opts.ReplyTo,
		Tags:    metadataToTags(msg.Metadata),
	}
	for _, a := range msg.Attachments {
		payload.Attachments = append(payload.Attachments, resendAttachment{
			Filename:    a.Name,
			ContentType: a.ContentType,
			Content:     a.Data,
		})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify/resend: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify/resend: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.opts.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("notify/resend: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("notify/resend: status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

// resendPayload mirrors Resend's /emails request body.
type resendPayload struct {
	From        string             `json:"from"`
	To          []string           `json:"to"`
	CC          []string           `json:"cc,omitempty"`
	BCC         []string           `json:"bcc,omitempty"`
	Subject     string             `json:"subject"`
	HTML        string             `json:"html,omitempty"`
	Text        string             `json:"text,omitempty"`
	ReplyTo     string             `json:"reply_to,omitempty"`
	Tags        []resendTag        `json:"tags,omitempty"`
	Attachments []resendAttachment `json:"attachments,omitempty"`
}

type resendTag struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type resendAttachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	// Resend accepts a base64-encoded `content` field; the JSON encoder
	// emits []byte as base64 automatically.
	Content []byte `json:"content"`
}

func metadataToTags(md map[string]string) []resendTag {
	if len(md) == 0 {
		return nil
	}
	out := make([]resendTag, 0, len(md))
	for k, v := range md {
		out = append(out, resendTag{Name: k, Value: v})
	}
	return out
}
