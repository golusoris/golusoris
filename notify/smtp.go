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
	c, err := mail.NewClient(opts.Host,
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
	from := msg.From
	if from == "" {
		from = s.opts.From
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
	m.Subject(msg.Subject)
	if msg.HTML != "" {
		m.SetBodyString(mail.TypeTextHTML, msg.HTML)
	}
	if msg.Text != "" {
		m.AddAlternativeString(mail.TypeTextPlain, msg.Text)
	}
	for _, a := range msg.Attachments {
		if err := m.AttachReader(a.Name, bytes.NewReader(a.Data), mail.WithFileEncoding(mail.EncodingB64)); err != nil {
			return fmt.Errorf("notify/smtp: attach %q: %w", a.Name, err)
		}
	}
	if err := s.client.DialAndSend(m); err != nil {
		return fmt.Errorf("notify/smtp: send: %w", err)
	}
	return nil
}
