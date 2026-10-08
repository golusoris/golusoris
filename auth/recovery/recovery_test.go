// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package recovery_test

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/recovery"
	gerr "github.com/golusoris/golusoris/core/errors"
)

var testHMACSecret = strings.Repeat("x", 32)

func TestNew_EmptySecret(t *testing.T) {
	t.Parallel()
	for _, size := range []int{0, 1, 31} {
		_, err := recovery.New(newMemCodeStore(), nil, nil, make([]byte, size))
		require.Error(t, err)
	}
	_, err := recovery.New(newMemCodeStore(), nil, nil, make([]byte, 32))
	require.NoError(t, err)
}

func TestNew_RequiresConfiguredStore(t *testing.T) {
	t.Parallel()
	_, err := recovery.New(nil, nil, nil, []byte(testHMACSecret))
	require.Error(t, err)
}

func TestNew_HandlesTypedNilDependencies(t *testing.T) {
	t.Parallel()

	t.Run("both stores", func(t *testing.T) {
		t.Parallel()
		var codes *memCodeStore
		var tokens *memTokenStore
		_, err := recovery.New(codes, tokens, nil, []byte(testHMACSecret))
		require.Error(t, err)
	})

	t.Run("optional code store", func(t *testing.T) {
		t.Parallel()
		var codes *memCodeStore
		svc, err := recovery.New(codes, newMemTokenStore(), nil, []byte(testHMACSecret))
		require.NoError(t, err)
		_, err = svc.IssueCodes(context.Background(), "u", 1)
		require.Error(t, err)
	})

	t.Run("optional clock", func(t *testing.T) {
		t.Parallel()
		var clk *clockwork.FakeClock
		svc, err := recovery.New(nil, newMemTokenStore(), clk, []byte(testHMACSecret))
		require.NoError(t, err)
		_, err = svc.IssueResetToken(context.Background(), "u", time.Minute)
		require.NoError(t, err)
	})
}

func TestService_RecoveryCodes(t *testing.T) {
	t.Parallel()

	cs := newMemCodeStore()
	svc, err := recovery.New(cs, nil, nil, []byte(testHMACSecret))
	require.NoError(t, err)

	codes, err := svc.IssueCodes(context.Background(), "u-1", 5)
	require.NoError(t, err)
	require.Len(t, codes, 5)
	for _, code := range codes {
		require.Len(t, code, 13)
		require.Regexp(t, `^[A-Z2-7]{13}$`, code)
	}

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
	svc, err := recovery.New(nil, ts, clk, []byte(testHMACSecret))
	require.NoError(t, err)

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
	svc, err := recovery.New(nil, ts, clk, []byte(testHMACSecret))
	require.NoError(t, err)

	raw, err := svc.IssueResetToken(context.Background(), "u-3", 5*time.Minute)
	require.NoError(t, err)

	clk.Advance(5 * time.Minute)
	_, err = svc.VerifyResetToken(context.Background(), raw)
	require.Error(t, err)
}

func TestService_RequiresStoreForSelectedFlow(t *testing.T) {
	t.Parallel()

	svc, err := recovery.New(newMemCodeStore(), nil, nil, []byte(testHMACSecret))
	require.NoError(t, err)
	_, err = svc.IssueResetToken(context.Background(), "u", time.Minute)
	require.Error(t, err)
	_, err = svc.VerifyResetToken(context.Background(), "token")
	require.Error(t, err)

	svc, err = recovery.New(nil, newMemTokenStore(), nil, []byte(testHMACSecret))
	require.NoError(t, err)
	_, err = svc.IssueCodes(context.Background(), "u", 1)
	require.Error(t, err)
	require.Error(t, svc.VerifyCode(context.Background(), "u", "code"))
}

func TestService_RejectsInvalidIssuance(t *testing.T) {
	t.Parallel()

	svc, err := recovery.New(newMemCodeStore(), newMemTokenStore(), nil, []byte(testHMACSecret))
	require.NoError(t, err)
	_, err = svc.IssueCodes(context.Background(), " ", 1)
	require.Error(t, err)
	for _, ttl := range []time.Duration{0, -time.Second} {
		_, issueErr := svc.IssueResetToken(context.Background(), "u", ttl)
		require.Error(t, issueErr)
	}
	_, err = svc.IssueResetToken(context.Background(), " ", time.Minute)
	require.Error(t, err)
}

func TestService_ClonesSecret(t *testing.T) {
	t.Parallel()

	secret := []byte(testHMACSecret)
	svc, err := recovery.New(nil, newMemTokenStore(), nil, secret)
	require.NoError(t, err)
	token, err := svc.IssueResetToken(context.Background(), "u", time.Minute)
	require.NoError(t, err)
	secret[0] = 'x'
	_, err = svc.VerifyResetToken(context.Background(), token)
	require.NoError(t, err)
}

func TestService_IssueCodeCountBoundaries(t *testing.T) {
	t.Parallel()

	svc, err := recovery.New(newMemCodeStore(), nil, nil, []byte(testHMACSecret))
	require.NoError(t, err)
	for _, count := range []int{0, 33} {
		_, issueErr := svc.IssueCodes(context.Background(), "u", count)
		require.Error(t, issueErr)
	}
}

func TestService_PropagatesSaveFailures(t *testing.T) {
	t.Parallel()

	svc, err := recovery.New(failingCodeStore{}, failingTokenStore{}, nil, []byte(testHMACSecret))
	require.NoError(t, err)
	_, err = svc.IssueCodes(context.Background(), "u", 1)
	require.ErrorIs(t, err, errStore)
	_, err = svc.IssueResetToken(context.Background(), "u", time.Minute)
	require.ErrorIs(t, err, errStore)
}

func TestService_RejectsMismatchedStoreRecords(t *testing.T) {
	t.Parallel()

	svc, err := recovery.New(mismatchedCodeStore{}, mismatchedTokenStore{}, nil, []byte(testHMACSecret))
	require.NoError(t, err)
	requireUnauthorized(t, svc.VerifyCode(context.Background(), "u", "code"))
	_, err = svc.VerifyResetToken(context.Background(), "token")
	requireUnauthorized(t, err)
}

func TestService_ConcurrentRecoveryCodeOnlyOneSucceeds(t *testing.T) {
	t.Parallel()

	store := newBarrierCodeStore(2)
	svc, err := recovery.New(store, nil, nil, []byte(testHMACSecret))
	require.NoError(t, err)
	codes, err := svc.IssueCodes(context.Background(), "u-code", 1)
	require.NoError(t, err)

	results := make(chan error, 2)
	for range 2 {
		go func() { results <- svc.VerifyCode(context.Background(), "u-code", codes[0]) }()
	}
	store.waitAndRelease()
	requireSingleSuccess(t, results, 2)
}

func TestService_ConcurrentResetTokenOnlyOneSucceeds(t *testing.T) {
	t.Parallel()

	store := newBarrierTokenStore(2)
	svc, err := recovery.New(nil, store, nil, []byte(testHMACSecret))
	require.NoError(t, err)
	token, err := svc.IssueResetToken(context.Background(), "u-token", time.Minute)
	require.NoError(t, err)

	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, verifyErr := svc.VerifyResetToken(context.Background(), token)
			results <- verifyErr
		}()
	}
	store.waitAndRelease()
	requireSingleSuccess(t, results, 2)
}

func requireSingleSuccess(t *testing.T, results <-chan error, total int) {
	t.Helper()
	successes := 0
	failures := 0
	for range total {
		if err := <-results; err == nil {
			successes++
		} else {
			requireUnauthorized(t, err)
			failures++
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, total-1, failures)
}

func requireUnauthorized(t *testing.T, err error) {
	t.Helper()
	var coded *gerr.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, gerr.CodeUnauthorized, coded.Code)
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

func (m *memCodeStore) Consume(_ context.Context, uid string, hash []byte) (recovery.Code, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.cs {
		if m.cs[i].UserID == uid && m.cs[i].UsedAt == nil && bytes.Equal(m.cs[i].Hash, hash) {
			now := time.Unix(1, 0)
			m.cs[i].UsedAt = &now
			return m.cs[i], nil
		}
	}
	return recovery.Code{}, errNotFound
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

func (m *memTokenStore) Consume(_ context.Context, hash []byte) (recovery.Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.ts {
		if m.ts[i].UsedAt == nil && bytes.Equal(m.ts[i].Hash, hash) {
			now := time.Unix(1, 0)
			m.ts[i].UsedAt = &now
			return m.ts[i], nil
		}
	}
	return recovery.Token{}, errNotFound
}

type errType string

func (e errType) Error() string { return string(e) }

const errNotFound errType = "not found"

const errStore errType = "store failed"

type failingCodeStore struct{}

func (failingCodeStore) SaveBatch(context.Context, []recovery.Code) error { return errStore }

func (failingCodeStore) Consume(context.Context, string, []byte) (recovery.Code, error) {
	return recovery.Code{}, errStore
}

type failingTokenStore struct{}

func (failingTokenStore) Save(context.Context, recovery.Token) error { return errStore }

func (failingTokenStore) Consume(context.Context, []byte) (recovery.Token, error) {
	return recovery.Token{}, errStore
}

type mismatchedCodeStore struct{}

func (mismatchedCodeStore) SaveBatch(context.Context, []recovery.Code) error { return nil }

func (mismatchedCodeStore) Consume(context.Context, string, []byte) (recovery.Code, error) {
	return recovery.Code{Hash: []byte("wrong")}, nil
}

type mismatchedTokenStore struct{}

func (mismatchedTokenStore) Save(context.Context, recovery.Token) error { return nil }

func (mismatchedTokenStore) Consume(context.Context, []byte) (recovery.Token, error) {
	return recovery.Token{Hash: []byte("wrong"), ExpiresAt: time.Unix(1<<32, 0)}, nil
}

type callBarrier struct {
	ready   chan struct{}
	release chan struct{}
}

func newCallBarrier(callers int) callBarrier {
	return callBarrier{ready: make(chan struct{}, callers), release: make(chan struct{})}
}

func (b *callBarrier) wait() {
	b.ready <- struct{}{}
	<-b.release
}

func (b *callBarrier) waitAndRelease() {
	for range cap(b.ready) {
		<-b.ready
	}
	close(b.release)
}

type barrierCodeStore struct {
	*memCodeStore
	callBarrier
}

func newBarrierCodeStore(callers int) *barrierCodeStore {
	return &barrierCodeStore{memCodeStore: newMemCodeStore(), callBarrier: newCallBarrier(callers)}
}

func (s *barrierCodeStore) Consume(ctx context.Context, uid string, hash []byte) (recovery.Code, error) {
	s.wait()
	return s.memCodeStore.Consume(ctx, uid, hash)
}

type barrierTokenStore struct {
	*memTokenStore
	callBarrier
}

func newBarrierTokenStore(callers int) *barrierTokenStore {
	return &barrierTokenStore{memTokenStore: newMemTokenStore(), callBarrier: newCallBarrier(callers)}
}

func (s *barrierTokenStore) Consume(ctx context.Context, hash []byte) (recovery.Token, error) {
	s.wait()
	return s.memTokenStore.Consume(ctx, hash)
}
