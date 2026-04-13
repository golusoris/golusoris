package notify_test

import (
	"context"
	"errors"
	"testing"

	"github.com/golusoris/golusoris/notify"
)

type mockSender struct {
	name string
	err  error
	got  []notify.Message
}

func (m *mockSender) Name() string { return m.name }
func (m *mockSender) Send(_ context.Context, msg notify.Message) error {
	m.got = append(m.got, msg)
	return m.err
}

func newNotifier(senders ...notify.Sender) *notify.Notifier {
	opts := make([]notify.Option, len(senders))
	for i, s := range senders {
		opts[i] = notify.WithSender(s)
	}
	return notify.New(discardLogger(), opts...)
}

func TestSendFirstSucceeds(t *testing.T) {
	t.Parallel()
	a := &mockSender{name: "a"}
	b := &mockSender{name: "b"}
	n := newNotifier(a, b)

	msg := notify.Message{To: []string{"x@example.com"}, Subject: "hi"}
	if err := n.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(a.got) != 1 {
		t.Errorf("sender a called %d times, want 1", len(a.got))
	}
	if len(b.got) != 0 {
		t.Errorf("sender b should not be called after a succeeds")
	}
}

func TestSendFallsThrough(t *testing.T) {
	t.Parallel()
	a := &mockSender{name: "a", err: errors.New("fail")}
	b := &mockSender{name: "b"}
	n := newNotifier(a, b)

	if err := n.Send(context.Background(), notify.Message{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(b.got) != 1 {
		t.Errorf("sender b called %d times after a failed, want 1", len(b.got))
	}
}

func TestSendAllFail(t *testing.T) {
	t.Parallel()
	fail := errors.New("fail")
	n := newNotifier(
		&mockSender{name: "a", err: fail},
		&mockSender{name: "b", err: fail},
	)
	if err := n.Send(context.Background(), notify.Message{}); err == nil {
		t.Error("expected error when all senders fail")
	}
}

func TestMultiSendsToAll(t *testing.T) {
	t.Parallel()
	a := &mockSender{name: "a"}
	b := &mockSender{name: "b"}
	n := newNotifier(a, b)

	errs := n.Multi(context.Background(), notify.Message{})
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(a.got) != 1 || len(b.got) != 1 {
		t.Errorf("expected both senders called, a=%d b=%d", len(a.got), len(b.got))
	}
}
