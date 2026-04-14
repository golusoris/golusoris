// Package sendgrid sends transactional email via SendGrid's v3 HTTP API.
//
// Raw HTTP — we don't pull in sendgrid/sendgrid-go because its SDK is
// large and the JSON wire format is small.
//
// Usage:
//
//	s, err := sendgrid.NewSender(sendgrid.Options{
//	    APIKey: os.Getenv("SENDGRID_API_KEY"),
//	    From:   "noreply@example.com",
//	})
//	notify.New(logger, notify.WithSender(s))
package sendgrid

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

// DefaultEndpoint is SendGrid's v3 mail/send endpoint.
const DefaultEndpoint = "https://api.sendgrid.com/v3/mail/send"

// Options configures the SendGrid sender.
type Options struct {
	// APIKey is a SendGrid API key (SG....). Required.
	APIKey string `koanf:"api_key"`
	// From is the default sender email address. Required.
	From string `koanf:"from"`
	// FromName is an optional display name for the From address.
	FromName string `koanf:"from_name"`
	// ReplyTo overrides the default reply-to.
	ReplyTo string `koanf:"reply_to"`
	// Endpoint overrides the API URL (tests).
	Endpoint string `koanf:"endpoint"`
	// HTTPClient is optional; defaults to a 10s-timeout client.
	HTTPClient *http.Client
}

// Sender posts notify.Messages to SendGrid.
type Sender struct {
	opts     Options
	endpoint string
	hc       *http.Client
}

// NewSender returns a SendGrid sender.
func NewSender(opts Options) (*Sender, error) {
	if opts.APIKey == "" {
		return nil, errors.New("notify/sendgrid: api_key is required")
	}
	if opts.From == "" {
		return nil, errors.New("notify/sendgrid: from is required")
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
func (s *Sender) Name() string { return "sendgrid" }

// Send implements [notify.Sender]. Maps notify.Message to the SendGrid
// v3 mail/send request body.
func (s *Sender) Send(ctx context.Context, msg notify.Message) error {
	if len(msg.To) == 0 {
		return errors.New("notify/sendgrid: at least one recipient required")
	}
	if msg.HTML == "" && msg.Text == "" {
		return errors.New("notify/sendgrid: html or text body required")
	}

	fromEmail := msg.From
	fromName := s.opts.FromName
	if fromEmail == "" {
		fromEmail = s.opts.From
	}

	personalization := sgPersonalization{
		To: emailAddresses(msg.To),
	}
	if len(msg.CC) > 0 {
		personalization.CC = emailAddresses(msg.CC)
	}
	if len(msg.BCC) > 0 {
		personalization.BCC = emailAddresses(msg.BCC)
	}
	payload := sgPayload{
		Personalizations: []sgPersonalization{personalization},
		From:             sgEmail{Email: fromEmail, Name: fromName},
		Subject:          msg.Subject,
	}
	if msg.Text != "" {
		payload.Content = append(payload.Content, sgContent{Type: "text/plain", Value: msg.Text})
	}
	if msg.HTML != "" {
		payload.Content = append(payload.Content, sgContent{Type: "text/html", Value: msg.HTML})
	}
	if s.opts.ReplyTo != "" {
		payload.ReplyTo = &sgEmail{Email: s.opts.ReplyTo}
	}
	if len(msg.Metadata) > 0 {
		payload.CustomArgs = msg.Metadata
	}
	for _, a := range msg.Attachments {
		payload.Attachments = append(payload.Attachments, sgAttachment{
			Content:  a.Data,
			Type:     a.ContentType,
			Filename: a.Name,
		})
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify/sendgrid: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify/sendgrid: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.opts.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("notify/sendgrid: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("notify/sendgrid: status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

func emailAddresses(in []string) []sgEmail {
	out := make([]sgEmail, 0, len(in))
	for _, e := range in {
		out = append(out, sgEmail{Email: e})
	}
	return out
}

// sgPayload mirrors SendGrid v3 mail/send request body.
type sgPayload struct {
	Personalizations []sgPersonalization `json:"personalizations"`
	From             sgEmail             `json:"from"`
	ReplyTo          *sgEmail            `json:"reply_to,omitempty"`
	Subject          string              `json:"subject"`
	Content          []sgContent         `json:"content"`
	Attachments      []sgAttachment      `json:"attachments,omitempty"`
	CustomArgs       map[string]string   `json:"custom_args,omitempty"`
}

type sgPersonalization struct {
	To  []sgEmail `json:"to"`
	CC  []sgEmail `json:"cc,omitempty"`
	BCC []sgEmail `json:"bcc,omitempty"`
}

type sgEmail struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type sgContent struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type sgAttachment struct {
	// Content is base64-encoded by the JSON encoder's []byte handling.
	Content  []byte `json:"content"`
	Type     string `json:"type,omitempty"`
	Filename string `json:"filename"`
}
