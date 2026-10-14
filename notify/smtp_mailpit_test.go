// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package notify_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/golusoris/golusoris/internal/testimages"
	"github.com/golusoris/golusoris/notify"
)

//nolint:tagliatelle // Mailpit API wire format uses PascalCase.
type mailpitAddress struct {
	Name    string `json:"Name"`
	Address string `json:"Address"`
}

//nolint:tagliatelle // Mailpit API wire format uses PascalCase.
type mailpitAttachment struct {
	PartID      string `json:"PartID"`
	FileName    string `json:"FileName"`
	ContentType string `json:"ContentType"`
}

//nolint:tagliatelle // Mailpit API wire format uses PascalCase.
type mailpitMessage struct {
	ID          string              `json:"ID"`
	Subject     string              `json:"Subject"`
	From        mailpitAddress      `json:"From"`
	To          []mailpitAddress    `json:"To"`
	Cc          []mailpitAddress    `json:"Cc"`
	Bcc         []mailpitAddress    `json:"Bcc"`
	Text        string              `json:"Text"`
	HTML        string              `json:"HTML"`
	Attachments []mailpitAttachment `json:"Attachments"`
}

//nolint:tagliatelle // Mailpit API wire format uses PascalCase.
type mailpitMessagesSummary struct {
	Total    int `json:"total"`
	Messages []struct {
		ID      string `json:"ID"`
		Subject string `json:"Subject"`
	} `json:"messages"`
}

// startMailpit boots a real Mailpit container and returns SMTP host, port, and HTTP base URL.
func startMailpit(t *testing.T) (string, int, string) {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        testimages.Mailpit,
			ReaperImage:  testimages.Ryuk,
			ExposedPorts: []string{"1025/tcp", "8025/tcp"},
			// Accept exactly one credential pair over plaintext SMTP.
			Env: map[string]string{
				"MP_SMTP_AUTH":                "probe:probe-secret",
				"MP_SMTP_AUTH_ALLOW_INSECURE": "true",
			},
			WaitingFor: wait.ForAll(
				wait.ForListeningPort("1025/tcp"),
				wait.ForListeningPort("8025/tcp"),
			).WithDeadline(1 * time.Minute),
		},
		Started: true,
	}
	container, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		t.Fatalf("notify: start mailpit container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer stopCancel()
		if termErr := container.Terminate(stopCtx); termErr != nil {
			t.Logf("notify: terminate mailpit container: %v", termErr)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("notify: container host: %v", err)
	}
	smtpPort, err := container.MappedPort(ctx, "1025/tcp")
	if err != nil {
		t.Fatalf("notify: mapped smtp port: %v", err)
	}
	httpPort, err := container.MappedPort(ctx, "8025/tcp")
	if err != nil {
		t.Fatalf("notify: mapped http port: %v", err)
	}
	smtpPortInt, err := strconv.Atoi(smtpPort.Port())
	if err != nil {
		t.Fatalf("notify: parse mapped smtp port: %v", err)
	}
	return host, smtpPortInt, "http://" + net.JoinHostPort(host, httpPort.Port())
}

func mailpitList(ctx context.Context, client *http.Client, httpBase string) (mailpitMessagesSummary, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpBase+"/api/v1/messages", nil)
	if err != nil {
		return mailpitMessagesSummary{}, fmt.Errorf("new request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return mailpitMessagesSummary{}, fmt.Errorf("list messages: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return mailpitMessagesSummary{}, fmt.Errorf("unexpected status: %s", resp.Status)
	}
	var summary mailpitMessagesSummary
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		return mailpitMessagesSummary{}, fmt.Errorf("decode summary: %w", err)
	}
	return summary, nil
}

func mailpitCount(ctx context.Context, client *http.Client, httpBase string) (int, error) {
	summary, err := mailpitList(ctx, client, httpBase)
	if err != nil {
		return 0, err
	}
	return summary.Total, nil
}

func mailpitMessageByID(ctx context.Context, client *http.Client, httpBase, id string) (mailpitMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpBase+"/api/v1/message/"+id, nil)
	if err != nil {
		return mailpitMessage{}, fmt.Errorf("new request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return mailpitMessage{}, fmt.Errorf("get message: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return mailpitMessage{}, fmt.Errorf("unexpected status: %s", resp.Status)
	}
	var msg mailpitMessage
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		return mailpitMessage{}, fmt.Errorf("decode message: %w", err)
	}
	return msg, nil
}

func mailpitFindMessageBySubject(ctx context.Context, client *http.Client, httpBase, subject string) (mailpitMessage, error) {
	for range 20 {
		summary, err := mailpitList(ctx, client, httpBase)
		if err != nil {
			return mailpitMessage{}, err
		}
		for _, m := range summary.Messages {
			if m.Subject == subject {
				return mailpitMessageByID(ctx, client, httpBase, m.ID)
			}
		}
		select {
		case <-ctx.Done():
			return mailpitMessage{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return mailpitMessage{}, fmt.Errorf("message with subject %q not found", subject)
}

func mailpitAttachmentBytes(ctx context.Context, client *http.Client, httpBase, id, partID string) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v1/message/%s/part/%s", httpBase, id, partID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get attachment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}
	return data, nil
}

// probeSender returns a sender for the Mailpit container with the given password and TLS policy.
func probeSender(t *testing.T, host string, port int, password string, insecure bool) *notify.SMTPSender {
	t.Helper()
	s, err := notify.NewSMTPSender(notify.SMTPOptions{
		Host:                   host,
		Port:                   port,
		Username:               "probe",
		Password:               password,
		From:                   "sender@example.test",
		AllowInsecurePlaintext: insecure,
		Timeout:                10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	return s
}

func assertPositiveRecipients(t *testing.T, msg mailpitMessage) {
	t.Helper()
	if msg.Subject != "mailpit probe" {
		t.Errorf("subject = %q, want mailpit probe", msg.Subject)
	}
	if msg.From.Address != "sender@example.test" {
		t.Errorf("from = %q, want sender@example.test", msg.From.Address)
	}
	if len(msg.To) != 2 || msg.To[0].Address != "to1@example.test" || msg.To[1].Address != "to2@example.test" {
		t.Errorf("to = %+v, want to1@example.test, to2@example.test", msg.To)
	}
	if len(msg.Cc) != 1 || msg.Cc[0].Address != "cc@example.test" {
		t.Errorf("cc = %+v, want cc@example.test", msg.Cc)
	}
	if len(msg.Bcc) != 1 || msg.Bcc[0].Address != "bcc@example.test" {
		t.Errorf("bcc = %+v, want bcc@example.test", msg.Bcc)
	}
}

func assertPositiveBodyAndAttachment(t *testing.T, ctx context.Context, client *http.Client, httpBase string, msg mailpitMessage) {
	t.Helper()
	if !strings.Contains(msg.Text, "plain body") {
		t.Errorf("text = %q, want to contain plain body", msg.Text)
	}
	if !strings.Contains(msg.HTML, "<p>html body</p>") {
		t.Errorf("html = %q, want to contain <p>html body</p>", msg.HTML)
	}
	if len(msg.Attachments) != 1 {
		t.Fatalf("attachments count = %d, want 1", len(msg.Attachments))
	}
	att := msg.Attachments[0]
	if att.FileName != "report.txt" || att.ContentType != "text/plain" {
		t.Errorf("attachment = %+v, want report.txt, text/plain", att)
	}
	data, err := mailpitAttachmentBytes(ctx, client, httpBase, msg.ID, att.PartID)
	if err != nil {
		t.Fatalf("mailpitAttachmentBytes: %v", err)
	}
	if string(data) != "attachment bytes" {
		t.Errorf("attachment bytes = %q, want 'attachment bytes'", string(data))
	}
}

func testPositive(t *testing.T, host string, port int, httpBase string, client *http.Client) {
	t.Helper()
	s := probeSender(t, host, port, "probe-secret", true)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err := s.Send(ctx, notify.Message{
		To:      []string{"to1@example.test", "to2@example.test"},
		CC:      []string{"cc@example.test"},
		BCC:     []string{"bcc@example.test"},
		Subject: "mailpit probe",
		Text:    "plain body",
		HTML:    "<p>html body</p>",
		Attachments: []notify.Attachment{{
			Name:        "report.txt",
			ContentType: "text/plain",
			Data:        []byte("attachment bytes"),
		}},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	msg, err := mailpitFindMessageBySubject(ctx, client, httpBase, "mailpit probe")
	if err != nil {
		t.Fatalf("find message: %v", err)
	}
	assertPositiveRecipients(t, msg)
	assertPositiveBodyAndAttachment(t, ctx, client, httpBase, msg)
}

func testBoundary(t *testing.T, host string, port int, httpBase string, client *http.Client) {
	t.Helper()
	s := probeSender(t, host, port, "probe-secret", true)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// HTML-only message generates text body via htmlToPlainText.
	err := s.Send(ctx, notify.Message{
		To:      []string{"to@example.test"},
		Subject: "boundary html only",
		HTML:    "<b>A &amp; B</b>",
	})
	if err != nil {
		t.Fatalf("Send html only: %v", err)
	}
	msgHTML, err := mailpitFindMessageBySubject(ctx, client, httpBase, "boundary html only")
	if err != nil {
		t.Fatalf("find html only: %v", err)
	}
	if strings.TrimSpace(msgHTML.Text) != "A & B" {
		t.Errorf("text = %q, want 'A & B'", msgHTML.Text)
	}

	// Body-only message populates plain text body.
	err = s.Send(ctx, notify.Message{
		To:      []string{"to@example.test"},
		Subject: "boundary body only",
		Body:    "body text only",
	})
	if err != nil {
		t.Fatalf("Send body only: %v", err)
	}
	msgBody, err := mailpitFindMessageBySubject(ctx, client, httpBase, "boundary body only")
	if err != nil {
		t.Fatalf("find body only: %v", err)
	}
	if strings.TrimSpace(msgBody.Text) != "body text only" {
		t.Errorf("text = %q, want 'body text only'", msgBody.Text)
	}
}

func testNegativeAuth(t *testing.T, host string, port int, httpBase string, client *http.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	countBefore, err := mailpitCount(ctx, client, httpBase)
	if err != nil {
		t.Fatalf("mailpitCount before: %v", err)
	}

	s := probeSender(t, host, port, "wrong", true)

	err = s.Send(ctx, notify.Message{
		To:      []string{"to@example.test"},
		Subject: "auth failure probe",
		Text:    "must not be sent",
	})
	if err == nil || !strings.Contains(err.Error(), "notify/smtp: send") {
		t.Fatalf("Send error = %v, want to contain 'notify/smtp: send'", err)
	}

	countAfter, err := mailpitCount(ctx, client, httpBase)
	if err != nil {
		t.Fatalf("mailpitCount after: %v", err)
	}
	if countAfter != countBefore {
		t.Errorf("message count = %d, want %d", countAfter, countBefore)
	}
}

func testNegativeTLS(t *testing.T, host string, port int, httpBase string, client *http.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	countBefore, err := mailpitCount(ctx, client, httpBase)
	if err != nil {
		t.Fatalf("mailpitCount before: %v", err)
	}

	s := probeSender(t, host, port, "probe-secret", false)

	err = s.Send(ctx, notify.Message{
		To:      []string{"to@example.test"},
		Subject: "tls failure probe",
		Text:    "must not be sent",
	})
	if err == nil {
		t.Fatal("Send error = nil, want TLS failure")
	}

	countAfter, err := mailpitCount(ctx, client, httpBase)
	if err != nil {
		t.Fatalf("mailpitCount after: %v", err)
	}
	if countAfter != countBefore {
		t.Errorf("message count = %d, want %d", countAfter, countBefore)
	}
}

func testNegativeConnect(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := listener.Addr()
	if closeErr := listener.Close(); closeErr != nil {
		t.Fatalf("listener.Close: %v", closeErr)
	}
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %T, want *net.TCPAddr", addr)
	}
	closedPort := tcpAddr.Port

	s, err := notify.NewSMTPSender(notify.SMTPOptions{
		Host:                   "127.0.0.1",
		Port:                   closedPort,
		AllowInsecurePlaintext: true,
		Timeout:                2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = s.Send(ctx, notify.Message{
		To:      []string{"to@example.test"},
		Subject: "connect failure probe",
		Text:    "must fail to connect",
	})
	if err == nil {
		t.Fatal("Send error = nil, want connect failure")
	}
}

//nolint:paralleltest,tparallel // subtests share one Mailpit container and run in order.
func TestSMTPSender_Mailpit(t *testing.T) {
	t.Parallel()
	host, port, httpBase := startMailpit(t)
	client := &http.Client{Timeout: 5 * time.Second}

	t.Run("positive", func(t *testing.T) {
		testPositive(t, host, port, httpBase, client)
	})
	t.Run("boundary", func(t *testing.T) {
		testBoundary(t, host, port, httpBase, client)
	})
	t.Run("negative_auth", func(t *testing.T) {
		testNegativeAuth(t, host, port, httpBase, client)
	})
	t.Run("negative_tls", func(t *testing.T) {
		testNegativeTLS(t, host, port, httpBase, client)
	})
	t.Run("negative_connect", func(t *testing.T) {
		testNegativeConnect(t)
	})
}
