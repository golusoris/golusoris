// Package inbound parses inbound-email webhooks from SES (via SNS) and
// Postmark, plus raw RFC 5322 MIME emails from SMTP servers.
// Each path normalizes to [Email], which the app consumes via a
// [HandlerFunc].
//
// Usage:
//
//	h := inbound.Handler(func(ctx context.Context, m inbound.Email) {
//	    log.Info("got mail", "from", m.From, "subject", m.Subject)
//	})
//	mux.Handle("/webhooks/ses-inbound",      inbound.SES(h))
//	mux.Handle("/webhooks/postmark-inbound", inbound.Postmark(h))
//
//	// SMTP handoff:
//	m, err := inbound.ParseMIME(raw)
package inbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"strings"
	"time"
)

// Email is a normalized inbound message.
type Email struct {
	MessageID string
	From      string
	To        []string
	CC        []string
	Subject   string
	Text      string
	HTML      string
	// RawHeaders preserves the full header list; useful for threading
	// (In-Reply-To, References) and DKIM/SPF/Authentication-Results.
	RawHeaders map[string][]string
	ReceivedAt time.Time
	Provider   string
}

// HandlerFunc receives parsed inbound emails.
type HandlerFunc func(ctx context.Context, m Email)

// maxBodyBytes caps webhook bodies. Raise via [MaxBodyBytes] before
// mounting if you need larger.
const maxBodyBytes = 25 << 20 // 25 MiB (typical provider cap)

// SES returns an http.Handler for AWS SES inbound email notifications
// delivered via SNS. When SES is configured with "action: SNS" the
// notification carries the full MIME blob inline as `content`. When
// configured with "action: S3", `content` is empty — apps must fetch
// the S3 object themselves.
func SES(h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := readBody(r)
		if err != nil {
			http.Error(w, "body error", http.StatusBadRequest)
			return
		}
		var env snsEnvelope
		if jerr := json.Unmarshal(body, &env); jerr != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		switch env.Type {
		case "SubscriptionConfirmation":
			w.WriteHeader(http.StatusOK)
			return
		case "Notification":
		default:
			http.Error(w, "unknown sns type", http.StatusBadRequest)
			return
		}
		var n sesInbound
		if jerr := json.Unmarshal([]byte(env.Message), &n); jerr != nil {
			http.Error(w, "invalid ses message", http.StatusBadRequest)
			return
		}
		if n.Content == "" {
			// S3-action: no inline content. App must fetch from S3 —
			// still fire the event with what we have so apps can log it.
			h(r.Context(), Email{
				MessageID:  n.Mail.MessageID,
				From:       firstString(n.Mail.Source),
				To:         n.Mail.Destination,
				ReceivedAt: n.Mail.Timestamp,
				Provider:   "ses",
			})
			w.WriteHeader(http.StatusOK)
			return
		}
		m, err := ParseMIME([]byte(n.Content))
		if err != nil {
			http.Error(w, "mime parse: "+err.Error(), http.StatusBadRequest)
			return
		}
		m.Provider = "ses"
		if m.MessageID == "" {
			m.MessageID = n.Mail.MessageID
		}
		h(r.Context(), m)
		w.WriteHeader(http.StatusOK)
	})
}

// Postmark returns an http.Handler for Postmark inbound email webhooks.
// See https://postmarkapp.com/developer/webhooks/inbound-webhook.
func Postmark(h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := readBody(r)
		if err != nil {
			http.Error(w, "body error", http.StatusBadRequest)
			return
		}
		var p postmarkInbound
		if jerr := json.Unmarshal(body, &p); jerr != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		to := make([]string, 0, len(p.ToFull))
		for _, a := range p.ToFull {
			to = append(to, a.Email)
		}
		cc := make([]string, 0, len(p.CcFull))
		for _, a := range p.CcFull {
			cc = append(cc, a.Email)
		}
		h(r.Context(), Email{
			MessageID:  p.MessageID,
			From:       p.FromFull.Email,
			To:         to,
			CC:         cc,
			Subject:    p.Subject,
			Text:       p.TextBody,
			HTML:       p.HTMLBody,
			RawHeaders: headersFromPostmark(p.Headers),
			ReceivedAt: p.Date,
			Provider:   "postmark",
		})
		w.WriteHeader(http.StatusOK)
	})
}

// ParseMIME parses a raw RFC 5322 message. Useful when accepting email
// directly from an SMTP server (e.g. via net/smtpserver).
func ParseMIME(raw []byte) (Email, error) {
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		return Email{}, fmt.Errorf("notify/inbound: parse mime: %w", err)
	}
	headers := make(map[string][]string, len(msg.Header))
	for k, v := range msg.Header {
		headers[k] = v
	}
	subject, _ := decodeHeader(msg.Header.Get("Subject"))
	receivedAt, _ := mail.ParseDate(msg.Header.Get("Date"))
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		return Email{}, fmt.Errorf("notify/inbound: read body: %w", err)
	}
	return Email{
		MessageID:  msg.Header.Get("Message-ID"),
		From:       msg.Header.Get("From"),
		To:         splitAddr(msg.Header.Get("To")),
		CC:         splitAddr(msg.Header.Get("Cc")),
		Subject:    subject,
		Text:       string(body),
		RawHeaders: headers,
		ReceivedAt: receivedAt,
	}, nil
}

func decodeHeader(s string) (string, error) {
	dec := new(mime.WordDecoder)
	out, err := dec.DecodeHeader(s)
	if err != nil {
		return s, fmt.Errorf("notify/inbound: decode header: %w", err)
	}
	return out, nil
}

func splitAddr(s string) []string {
	if s == "" {
		return nil
	}
	addrs, err := mail.ParseAddressList(s)
	if err != nil {
		return []string{s}
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.Address)
	}
	return out
}

func headersFromPostmark(h []postmarkHeader) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string, len(h))
	for _, hdr := range h {
		out[hdr.Name] = append(out[hdr.Name], hdr.Value)
	}
	return out
}

func readBody(r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	defer func() { _ = r.Body.Close() }()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("notify/inbound: read body: %w", err)
	}
	if len(b) == 0 {
		return nil, errors.New("notify/inbound: empty body")
	}
	return b, nil
}

func firstString(s string) string { return s }

// snsEnvelope is the SNS delivery wrapper. See
// https://docs.aws.amazon.com/sns/latest/dg/sns-message-and-json-formats.html.
type snsEnvelope struct {
	Type    string `json:"Type"`
	Message string `json:"Message"`
}

type sesInbound struct {
	Mail struct {
		MessageID   string    `json:"messageId"`
		Source      string    `json:"source"`
		Destination []string  `json:"destination"`
		Timestamp   time.Time `json:"timestamp"`
	} `json:"mail"`
	// Content is the raw MIME blob (base64-wrapped by SES). Present only
	// when SES rule action is "SNS"; empty for "S3".
	Content string `json:"content"`
}

type postmarkInbound struct {
	MessageID string           `json:"MessageID"`
	Date      time.Time        `json:"Date"`
	Subject   string           `json:"Subject"`
	FromFull  postmarkAddr     `json:"FromFull"`
	ToFull    []postmarkAddr   `json:"ToFull"`
	CcFull    []postmarkAddr   `json:"CcFull"`
	TextBody  string           `json:"TextBody"`
	HTMLBody  string           `json:"HtmlBody"`
	Headers   []postmarkHeader `json:"Headers"`
}

type postmarkAddr struct {
	Email string `json:"Email"`
	Name  string `json:"Name"`
}

type postmarkHeader struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}
