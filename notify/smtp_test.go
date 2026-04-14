package notify_test

import (
	"testing"

	"github.com/golusoris/golusoris/notify"
)

func TestNewSMTPSender_Name(t *testing.T) {
	t.Parallel()
	s, err := notify.NewSMTPSender(notify.SMTPOptions{
		Host: "localhost",
		Port: 1025,
		TLS:  false,
	})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	if got := s.Name(); got != "smtp" {
		t.Errorf("Name() = %q, want smtp", got)
	}
}

func TestNewSMTPSender_DefaultPort(t *testing.T) {
	t.Parallel()
	// Port 0 triggers the default (587) code path.
	s, err := notify.NewSMTPSender(notify.SMTPOptions{
		Host: "localhost",
		Port: 0,
		TLS:  false,
	})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	if got := s.Name(); got != "smtp" {
		t.Errorf("Name() = %q, want smtp", got)
	}
}
