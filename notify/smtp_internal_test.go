// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package notify

import (
	"testing"

	"github.com/stretchr/testify/require"
	mail "github.com/wneessen/go-mail"
)

// TestApplyRecipients_UsesDefaultFrom is the positive case: an empty
// msg.From falls back to the sender's configured default, and To/Cc/Bcc
// are all applied.
func TestApplyRecipients_UsesDefaultFrom(t *testing.T) {
	t.Parallel()
	m := mail.NewMsg()
	msg := Message{
		To:  []string{"to@example.com"},
		CC:  []string{"cc@example.com"},
		BCC: []string{"bcc@example.com"},
	}
	require.NoError(t, applyRecipients(m, msg, "default@example.com"))
	require.Equal(t, []string{"<default@example.com>"}, m.GetFromString())
	require.Equal(t, []string{"<to@example.com>"}, m.GetToString())
	require.Equal(t, []string{"<cc@example.com>"}, m.GetCcString())
	require.Equal(t, []string{"<bcc@example.com>"}, m.GetBccString())
}

// TestApplyRecipients_MsgFromOverridesDefault is the boundary case for
// the From default: a non-empty msg.From wins over defaultFrom, and
// omitted Cc/Bcc stay unset.
func TestApplyRecipients_MsgFromOverridesDefault(t *testing.T) {
	t.Parallel()
	m := mail.NewMsg()
	msg := Message{From: "explicit@example.com", To: []string{"to@example.com"}}
	require.NoError(t, applyRecipients(m, msg, "default@example.com"))
	require.Equal(t, []string{"<explicit@example.com>"}, m.GetFromString())
	require.Empty(t, m.GetCcString())
	require.Empty(t, m.GetBccString())
}

// TestApplyRecipients_RejectsInvalidFrom is a negative case: an
// RFC-5322-invalid From address must surface as a wrapped "from" error.
func TestApplyRecipients_RejectsInvalidFrom(t *testing.T) {
	t.Parallel()
	m := mail.NewMsg()
	msg := Message{From: "not-an-email", To: []string{"to@example.com"}}
	err := applyRecipients(m, msg, "default@example.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "notify/smtp: from:")
}

// TestApplyRecipients_RejectsInvalidTo is a negative case for the To
// validation path.
func TestApplyRecipients_RejectsInvalidTo(t *testing.T) {
	t.Parallel()
	m := mail.NewMsg()
	msg := Message{From: "from@example.com", To: []string{"not-an-email"}}
	err := applyRecipients(m, msg, "default@example.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "notify/smtp: to:")
}

// TestApplyRecipients_RejectsInvalidCC is a negative case for the Cc
// validation path, only reached when Cc is non-empty.
func TestApplyRecipients_RejectsInvalidCC(t *testing.T) {
	t.Parallel()
	m := mail.NewMsg()
	msg := Message{From: "from@example.com", To: []string{"to@example.com"}, CC: []string{"not-an-email"}}
	err := applyRecipients(m, msg, "default@example.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "notify/smtp: cc:")
}

// TestApplyRecipients_RejectsInvalidBCC is a negative case for the Bcc
// validation path, only reached when Bcc is non-empty.
func TestApplyRecipients_RejectsInvalidBCC(t *testing.T) {
	t.Parallel()
	m := mail.NewMsg()
	msg := Message{From: "from@example.com", To: []string{"to@example.com"}, BCC: []string{"not-an-email"}}
	err := applyRecipients(m, msg, "default@example.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "notify/smtp: bcc:")
}

// TestApplyBody_SetsHTMLAndTextParts is the positive case: both HTML
// and Text produce a primary body plus a plain-text alternative part.
func TestApplyBody_SetsHTMLAndTextParts(t *testing.T) {
	t.Parallel()
	m := mail.NewMsg()
	applyBody(m, Message{Subject: "Hi", HTML: "<p>hi</p>", Text: "hi"})
	require.Equal(t, []string{"Hi"}, m.GetGenHeader(mail.HeaderSubject))

	parts := m.GetParts()
	require.Len(t, parts, 2)
	content0, err := parts[0].GetContent()
	require.NoError(t, err)
	require.Equal(t, "<p>hi</p>", string(content0))
	require.Equal(t, mail.TypeTextHTML, parts[0].GetContentType())
	content1, err := parts[1].GetContent()
	require.NoError(t, err)
	require.Equal(t, "hi", string(content1))
	require.Equal(t, mail.TypeTextPlain, parts[1].GetContentType())
}

// TestApplyBody_NoBodySetsSubjectOnly is the boundary case: with
// neither HTML nor Text, only the Subject header is set and no body
// parts are added.
func TestApplyBody_NoBodySetsSubjectOnly(t *testing.T) {
	t.Parallel()
	m := mail.NewMsg()
	applyBody(m, Message{Subject: "Empty body"})
	require.Equal(t, []string{"Empty body"}, m.GetGenHeader(mail.HeaderSubject))
	require.Empty(t, m.GetParts())
}

// TestAttachFiles_AddsEachAttachment is the positive case: every
// attachment in the slice is added, in order, base64-encoded.
func TestAttachFiles_AddsEachAttachment(t *testing.T) {
	t.Parallel()
	m := mail.NewMsg()
	err := attachFiles(m, []Attachment{
		{Name: "a.txt", Data: []byte("a")},
		{Name: "b.txt", Data: []byte("b")},
	})
	require.NoError(t, err)
	got := m.GetAttachments()
	require.Len(t, got, 2)
	require.Equal(t, "a.txt", got[0].Name)
	require.Equal(t, "b.txt", got[1].Name)
}

// TestAttachFiles_EmptyIsNoOp is the boundary case: an empty
// attachment slice adds nothing and returns no error.
func TestAttachFiles_EmptyIsNoOp(t *testing.T) {
	t.Parallel()
	m := mail.NewMsg()
	require.NoError(t, attachFiles(m, nil))
	require.Empty(t, m.GetAttachments())
}
