// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package notify_test

import (
	"testing"

	"github.com/golusoris/golusoris/notify"
)

func TestNewSMTPSender_Name(t *testing.T) {
	t.Parallel()
	s, err := notify.NewSMTPSender(notify.SMTPOptions{
		Host:                   "localhost",
		Port:                   1025,
		AllowInsecurePlaintext: true,
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
	// Port 0 triggers the secure default (587) code path.
	s, err := notify.NewSMTPSender(notify.SMTPOptions{
		Host: "localhost",
		Port: 0,
	})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	if got := s.Name(); got != "smtp" {
		t.Errorf("Name() = %q, want smtp", got)
	}
}
