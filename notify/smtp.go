// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package notify

import (
	"bytes"
	"context"
	"fmt"

	mail "github.com/wneessen/go-mail"
)

// SMTPOptions configures the SMTP sender.
type SMTPOptions struct {
	Host     string `koanf:"host"`
	Port     int    `koanf:"port"`
	Username string `koanf:"username"`
	Password string `koanf:"password"`
	// From is the default sender address.
	From string `koanf:"from"`
	// TLS enables STARTTLS (port 587) or implicit TLS (port 465).
	// Default true.
	TLS bool `koanf:"tls"`
}

// SMTPSender sends email via SMTP using go-mail.
type SMTPSender struct {
	opts   SMTPOptions
	client *mail.Client
}

// NewSMTPSender returns an SMTPSender. The connection is established
// lazily per message (go-mail manages pooling internally).
func NewSMTPSender(opts SMTPOptions) (*SMTPSender, error) {
	if opts.Port == 0 {
		opts.Port = 587
	}
	tlsPolicy := mail.TLSMandatory
	if !opts.TLS {
		tlsPolicy = mail.NoTLS
	}
	c, err := mail.NewClient(
		opts.Host,
		mail.WithPort(opts.Port),
		mail.WithSMTPAuth(mail.SMTPAuthPlain),
		mail.WithUsername(opts.Username),
		mail.WithPassword(opts.Password),
		mail.WithTLSPolicy(tlsPolicy),
	)
	if err != nil {
		return nil, fmt.Errorf("notify/smtp: new client: %w", err)
	}
	return &SMTPSender{opts: opts, client: c}, nil
}

// Name implements [Sender].
func (s *SMTPSender) Name() string { return "smtp" }

// Send implements [Sender].
func (s *SMTPSender) Send(_ context.Context, msg Message) error {
	m := mail.NewMsg()
	if err := applyRecipients(m, msg, s.opts.From); err != nil {
		return err
	}
	applyBody(m, msg)
	if err := attachFiles(m, msg.Attachments); err != nil {
		return err
	}
	if err := s.client.DialAndSend(m); err != nil {
		return fmt.Errorf("notify/smtp: send: %w", err)
	}
	return nil
}

// applyRecipients sets From/To/Cc/Bcc on m, defaulting From to
// defaultFrom when msg.From is empty. Cc/Bcc are set only when
// present — go-mail rejects an empty address list.
func applyRecipients(m *mail.Msg, msg Message, defaultFrom string) error {
	from := msg.From
	if from == "" {
		from = defaultFrom
	}
	if err := m.From(from); err != nil {
		return fmt.Errorf("notify/smtp: from: %w", err)
	}
	if err := m.To(msg.To...); err != nil {
		return fmt.Errorf("notify/smtp: to: %w", err)
	}
	if len(msg.CC) > 0 {
		if err := m.Cc(msg.CC...); err != nil {
			return fmt.Errorf("notify/smtp: cc: %w", err)
		}
	}
	if len(msg.BCC) > 0 {
		if err := m.Bcc(msg.BCC...); err != nil {
			return fmt.Errorf("notify/smtp: bcc: %w", err)
		}
	}
	return nil
}

// applyBody sets the subject and HTML/plain-text bodies. When both are
// set, HTML is the primary body and Text is attached as the
// plain-text alternative part.
func applyBody(m *mail.Msg, msg Message) {
	m.Subject(msg.Subject)
	if msg.HTML != "" {
		m.SetBodyString(mail.TypeTextHTML, msg.HTML)
	}
	if msg.Text != "" {
		m.AddAlternativeString(mail.TypeTextPlain, msg.Text)
	}
}

// attachFiles adds each attachment to m, base64-encoded.
func attachFiles(m *mail.Msg, attachments []Attachment) error {
	for _, a := range attachments {
		if err := m.AttachReader(a.Name, bytes.NewReader(a.Data), mail.WithFileEncoding(mail.EncodingB64)); err != nil {
			return fmt.Errorf("notify/smtp: attach %q: %w", a.Name, err)
		}
	}
	return nil
}
