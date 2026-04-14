// Package twilio sends SMS via Twilio's REST API.
//
// Raw HTTP — we don't pull in twilio-go because its SDK covers 40+
// products we don't use. The Messages endpoint is small enough to hand-
// code.
//
// Usage:
//
//	s, err := twilio.NewSender(twilio.Options{
//	    AccountSID: "AC...",
//	    AuthToken:  os.Getenv("TWILIO_AUTH_TOKEN"),
//	    From:       "+15551234567",
//	})
//	notify.New(logger, notify.WithSender(s))
package twilio

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

// DefaultEndpoint is Twilio's REST API base.
const DefaultEndpoint = "https://api.twilio.com/2010-04-01"

// Options configures the Twilio SMS sender.
type Options struct {
	// AccountSID is the Twilio account identifier (AC…). Required.
	AccountSID string `koanf:"account_sid"`
	// AuthToken is the Twilio auth token. Required.
	AuthToken string `koanf:"auth_token"`
	// From is the default sender phone number (E.164, e.g. "+15551234567")
	// or a Twilio short code. Exactly one of From or MessagingServiceSID
	// must be set.
	From string `koanf:"from"`
	// MessagingServiceSID routes through a Twilio Messaging Service
	// (MG…) instead of a fixed From. Mutually exclusive with From.
	MessagingServiceSID string `koanf:"messaging_service_sid"`
	// StatusCallback is an optional status-change webhook URL.
	StatusCallback string `koanf:"status_callback"`
	// Endpoint overrides the API base (tests).
	Endpoint string `koanf:"endpoint"`
	// HTTPClient is optional; defaults to a 10s-timeout client.
	HTTPClient *http.Client
}

// Sender posts notify.Messages to Twilio's Messages endpoint.
type Sender struct {
	opts     Options
	endpoint string
	hc       *http.Client
}

// NewSender returns a Twilio SMS sender.
func NewSender(opts Options) (*Sender, error) {
	if opts.AccountSID == "" {
		return nil, errors.New("notify/twilio: account_sid is required")
	}
	if opts.AuthToken == "" {
		return nil, errors.New("notify/twilio: auth_token is required")
	}
	if opts.From == "" && opts.MessagingServiceSID == "" {
		return nil, errors.New("notify/twilio: from or messaging_service_sid is required")
	}
	if opts.From != "" && opts.MessagingServiceSID != "" {
		return nil, errors.New("notify/twilio: from and messaging_service_sid are mutually exclusive")
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
func (s *Sender) Name() string { return "twilio" }

// Send implements [notify.Sender]. Each recipient in msg.To gets a
// separate SMS. The body is msg.Body (falling back to msg.Text, then
// msg.Subject).
func (s *Sender) Send(ctx context.Context, msg notify.Message) error {
	if len(msg.To) == 0 {
		return errors.New("notify/twilio: at least one recipient required")
	}
	body := msg.Body
	if body == "" {
		body = msg.Text
	}
	if body == "" {
		body = msg.Subject
	}
	if body == "" {
		return errors.New("notify/twilio: body required")
	}

	target := strings.TrimRight(s.endpoint, "/") + "/Accounts/" + s.opts.AccountSID + "/Messages.json"

	for _, to := range msg.To {
		form := url.Values{}
		form.Set("To", to)
		form.Set("Body", body)
		if s.opts.MessagingServiceSID != "" {
			form.Set("MessagingServiceSid", s.opts.MessagingServiceSID)
		} else {
			form.Set("From", s.opts.From)
		}
		if s.opts.StatusCallback != "" {
			form.Set("StatusCallback", s.opts.StatusCallback)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewBufferString(form.Encode()))
		if err != nil {
			return fmt.Errorf("notify/twilio: new request: %w", err)
		}
		req.SetBasicAuth(s.opts.AccountSID, s.opts.AuthToken)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := s.hc.Do(req)
		if err != nil {
			return fmt.Errorf("notify/twilio: post: %w", err)
		}
		if resp.StatusCode/100 != 2 {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
			_ = resp.Body.Close()
			return fmt.Errorf("notify/twilio: status %d for %s: %s", resp.StatusCode, to, respBody)
		}
		_ = resp.Body.Close()
	}
	return nil
}
