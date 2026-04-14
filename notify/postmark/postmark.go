// Package postmark sends transactional email via Postmark's HTTP API.
//
// Raw HTTP — no SDK. The Postmark wire format is small enough that we
// avoid pulling mrz1836/postmark and keep the dep graph lean.
//
// Usage:
//
//	s, err := postmark.NewSender(postmark.Options{
//	    ServerToken: os.Getenv("POSTMARK_SERVER_TOKEN"),
//	    From:        "noreply@example.com",
//	})
//	notify.New(logger, notify.WithSender(s))
package postmark

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

// DefaultEndpoint is Postmark's REST API base for /email.
const DefaultEndpoint = "https://api.postmarkapp.com/email"

// Options configures the Postmark sender.
type Options struct {
	// ServerToken is the per-server token from Postmark's dashboard.
	// Required.
	ServerToken string `koanf:"server_token"`
	// From is the default sender address — must be a verified Sender
	// Signature in Postmark.
	From string `koanf:"from"`
	// ReplyTo overrides the default reply-to header.
	ReplyTo string `koanf:"reply_to"`
	// MessageStream picks the message stream (e.g. "outbound" for
	// transactional, "broadcast" for marketing). Optional.
	MessageStream string `koanf:"message_stream"`
	// Endpoint overrides the API base — useful for tests.
	Endpoint string `koanf:"endpoint"`
	// HTTPClient is the optional HTTP client; defaults to a 10s-timeout
	// client.
	HTTPClient *http.Client
}

// Sender posts notify.Messages to Postmark's /email endpoint.
type Sender struct {
	opts     Options
	endpoint string
	hc       *http.Client
}

// NewSender returns a Postmark sender. Returns an error if ServerToken
// or From is empty.
func NewSender(opts Options) (*Sender, error) {
	if opts.ServerToken == "" {
		return nil, errors.New("notify/postmark: server_token is required")
	}
	if opts.From == "" {
		return nil, errors.New("notify/postmark: from is required")
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
func (s *Sender) Name() string { return "postmark" }

// Send implements [notify.Sender]. Maps notify.Message to the Postmark
// /email request body. Postmark requires HTML or Text — the call
// returns an error if both are empty.
func (s *Sender) Send(ctx context.Context, msg notify.Message) error {
	if len(msg.To) == 0 {
		return errors.New("notify/postmark: at least one recipient required")
	}
	if msg.HTML == "" && msg.Text == "" {
		return errors.New("notify/postmark: html or text body required")
	}
	from := msg.From
	if from == "" {
		from = s.opts.From
	}
	payload := postmarkPayload{
		From:          from,
		To:            strings.Join(msg.To, ","),
		CC:            strings.Join(msg.CC, ","),
		BCC:           strings.Join(msg.BCC, ","),
		Subject:       msg.Subject,
		HTMLBody:      msg.HTML,
		TextBody:      msg.Text,
		ReplyTo:       s.opts.ReplyTo,
		MessageStream: s.opts.MessageStream,
		Metadata:      msg.Metadata,
	}
	for _, a := range msg.Attachments {
		payload.Attachments = append(payload.Attachments, postmarkAttachment{
			Name:        a.Name,
			ContentType: a.ContentType,
			Content:     a.Data,
		})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify/postmark: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify/postmark: new request: %w", err)
	}
	req.Header.Set("X-Postmark-Server-Token", s.opts.ServerToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("notify/postmark: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("notify/postmark: status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

// postmarkPayload mirrors Postmark's /email request body.
type postmarkPayload struct {
	From          string               `json:"From"`
	To            string               `json:"To"`
	CC            string               `json:"Cc,omitempty"`
	BCC           string               `json:"Bcc,omitempty"`
	Subject       string               `json:"Subject"`
	HTMLBody      string               `json:"HtmlBody,omitempty"`
	TextBody      string               `json:"TextBody,omitempty"`
	ReplyTo       string               `json:"ReplyTo,omitempty"`
	MessageStream string               `json:"MessageStream,omitempty"`
	Metadata      map[string]string    `json:"Metadata,omitempty"`
	Attachments   []postmarkAttachment `json:"Attachments,omitempty"`
}

type postmarkAttachment struct {
	Name        string `json:"Name"`
	ContentType string `json:"ContentType,omitempty"`
	// Postmark accepts a base64-encoded `Content` field; the JSON
	// encoder emits []byte as base64 automatically.
	Content []byte `json:"Content"`
}
