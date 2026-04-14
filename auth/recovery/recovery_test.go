package recovery_test

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/recovery"
)

func TestService_RecoveryCodes(t *testing.T) {
	t.Parallel()

	cs := newMemCodeStore()
	svc := recovery.New(cs, nil, nil, []byte("k"))

	codes, err := svc.IssueCodes(context.Background(), "u-1", 5)
	require.NoError(t, err)
	require.Len(t, codes, 5)

	// Each code works once, then is rejected.
	require.NoError(t, svc.VerifyCode(context.Background(), "u-1", codes[0]))
	require.Error(t, svc.VerifyCode(context.Background(), "u-1", codes[0]))

	// A bogus code is rejected.
	require.Error(t, svc.VerifyCode(context.Background(), "u-1", "garbage"))
}

func TestService_ResetToken(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	ts := newMemTokenStore()
	svc := recovery.New(nil, ts, clk, []byte("k"))

	raw, err := svc.IssueResetToken(context.Background(), "u-2", 5*time.Minute)
	require.NoError(t, err)

	uid, err := svc.VerifyResetToken(context.Background(), raw)
	require.NoError(t, err)
	require.Equal(t, "u-2", uid)

	// Replay rejected.
	_, err = svc.VerifyResetToken(context.Background(), raw)
	require.Error(t, err)
}

func TestService_ResetTokenExpires(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	ts := newMemTokenStore()
	svc := recovery.New(nil, ts, clk, []byte("k"))

	raw, err := svc.IssueResetToken(context.Background(), "u-3", 5*time.Minute)
	require.NoError(t, err)

	clk.Advance(6 * time.Minute)
	_, err = svc.VerifyResetToken(context.Background(), raw)
	require.Error(t, err)
}

// --- in-memory stores ---

type memCodeStore struct {
	mu sync.Mutex
	cs []recovery.Code
}

func newMemCodeStore() *memCodeStore { return &memCodeStore{} }

func (m *memCodeStore) SaveBatch(_ context.Context, c []recovery.Code) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cs = append(m.cs, c...)
	return nil
}

func (m *memCodeStore) FindForUser(_ context.Context, uid string) ([]recovery.Code, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]recovery.Code, 0)
	for _, c := range m.cs {
		if c.UserID == uid {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *memCodeStore) MarkUsed(_ context.Context, uid string, hash []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for i := range m.cs {
		if m.cs[i].UserID == uid && bytes.Equal(m.cs[i].Hash, hash) {
			m.cs[i].UsedAt = &now
		}
	}
	return nil
}

type memTokenStore struct {
	mu sync.Mutex
	ts []recovery.Token
}

func newMemTokenStore() *memTokenStore { return &memTokenStore{} }

func (m *memTokenStore) Save(_ context.Context, t recovery.Token) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ts = append(m.ts, t)
	return nil
}

func (m *memTokenStore) Find(_ context.Context, hash []byte) (recovery.Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.ts {
		if bytes.Equal(t.Hash, hash) {
			return t, nil
		}
	}
	return recovery.Token{}, errNotFound
}

func (m *memTokenStore) MarkUsed(_ context.Context, hash []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for i := range m.ts {
		if bytes.Equal(m.ts[i].Hash, hash) {
			m.ts[i].UsedAt = &now
		}
	}
	return nil
}

type errType string

func (e errType) Error() string { return string(e) }

const errNotFound errType = "not found"
