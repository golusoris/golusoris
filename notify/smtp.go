// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"mime"
	"strings"
	"time"

	mail "github.com/wneessen/go-mail"
)

type smtpClient interface {
	DialAndSendWithContext(context.Context, ...*mail.Msg) error
}

// SMTPOptions configures the SMTP sender.
type SMTPOptions struct {
	Host     string `koanf:"host"`
	Port     int    `koanf:"port"`
	Username string `koanf:"username"`
	Password string `koanf:"password"`
	// From is the default sender address.
	From string `koanf:"from"`
	// AllowInsecurePlaintext disables transport encryption. Keep false outside
	// isolated development networks.
	AllowInsecurePlaintext bool `koanf:"allow_insecure_plaintext"`
	// Timeout bounds SMTP connection and delivery I/O. Default 15 seconds.
	Timeout time.Duration `koanf:"timeout"`
}

// SMTPSender sends email via SMTP using go-mail.
type SMTPSender struct {
	opts   SMTPOptions
	client smtpClient
}

// NewSMTPSender returns an SMTPSender. The connection is established
// lazily per message (go-mail manages pooling internally).
func NewSMTPSender(opts SMTPOptions) (*SMTPSender, error) {
	if opts.Timeout < 0 {
		return nil, errors.New("notify/smtp: timeout must not be negative")
	}
	if opts.Timeout == 0 {
		opts.Timeout = mail.DefaultTimeout
	}
	if opts.Port == 0 {
		opts.Port = 587
		if opts.AllowInsecurePlaintext {
			opts.Port = 25
		}
	}
	tlsPolicy := mail.TLSMandatory
	if opts.AllowInsecurePlaintext {
		tlsPolicy = mail.NoTLS
	}
	clientOpts := []mail.Option{
		mail.WithPort(opts.Port),
		mail.WithSMTPAuth(mail.SMTPAuthPlain),
		mail.WithUsername(opts.Username),
		mail.WithPassword(opts.Password),
		mail.WithTLSPolicy(tlsPolicy),
		mail.WithTimeout(opts.Timeout),
	}
	if opts.Port == 465 && !opts.AllowInsecurePlaintext {
		clientOpts = append(clientOpts, mail.WithSSL())
	}
	c, err := mail.NewClient(
		opts.Host,
		clientOpts...,
	)
	if err != nil {
		return nil, fmt.Errorf("notify/smtp: new client: %w", err)
	}
	return &SMTPSender{opts: opts, client: c}, nil
}

// Name implements [Sender].
func (s *SMTPSender) Name() string { return "smtp" }

// Send implements [Sender].
func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	if ctx == nil {
		return errors.New("notify/smtp: context is required")
	}
	m := mail.NewMsg()
	if err := applyRecipients(m, msg, s.opts.From); err != nil {
		return err
	}
	applyBody(m, msg)
	if err := attachFiles(m, msg.Attachments); err != nil {
		return err
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
	defer cancel()
	if err := s.client.DialAndSendWithContext(deliveryCtx, m); err != nil {
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

// applyBody sets the subject and plain-text/HTML bodies.
func applyBody(m *mail.Msg, msg Message) {
	m.Subject(msg.Subject)
	plain := msg.Text
	if plain == "" && msg.HTML != "" {
		plain = htmlToPlainText(msg.HTML)
	}
	if plain == "" {
		plain = msg.Body
	}
	if plain != "" || msg.HTML != "" {
		m.SetBodyString(mail.TypeTextPlain, plain)
	}
	if msg.HTML != "" {
		m.AddAlternativeString(mail.TypeTextHTML, msg.HTML)
	}
}

func htmlToPlainText(value string) string {
	text := make([]byte, 0, len(value))
	inTag := false
	var quote byte
	for idx := range len(value) {
		current := value[idx]
		if !inTag {
			if current == '<' {
				inTag = true
				continue
			}
			text = append(text, current)
			continue
		}
		if quote != 0 {
			if current == quote {
				quote = 0
			}
			continue
		}
		switch current {
		case '\'', '"':
			quote = current
		case '>':
			inTag = false
			text = append(text, ' ')
		}
	}
	return strings.Join(strings.Fields(html.UnescapeString(string(text))), " ")
}

// attachFiles adds each attachment to m, base64-encoded.
func attachFiles(m *mail.Msg, attachments []Attachment) error {
	for _, a := range attachments {
		fileOpts := []mail.FileOption{mail.WithFileEncoding(mail.EncodingB64)}
		if a.ContentType != "" {
			if _, _, err := mime.ParseMediaType(a.ContentType); err != nil {
				return fmt.Errorf("notify/smtp: attachment %q content type: %w", a.Name, err)
			}
			fileOpts = append(fileOpts, mail.WithFileContentType(mail.ContentType(a.ContentType)))
		}
		if err := m.AttachReader(a.Name, bytes.NewReader(a.Data), fileOpts...); err != nil {
			return fmt.Errorf("notify/smtp: attach %q: %w", a.Name, err)
		}
	}
	return nil
}
