// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package magiclink_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/magiclink"
)

func TestNew_EmptySecret(t *testing.T) {
	t.Parallel()
	_, err := magiclink.New(magiclink.NewMemoryStore(), nil, nil, 0)
	require.Error(t, err)
}

func TestService_HappyPath(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	svc, err := magiclink.New(magiclink.NewMemoryStore(), clk, []byte("k"), 5*time.Minute)
	require.NoError(t, err)

	tok, err := svc.Issue(context.Background(), "alice@example.com")
	require.NoError(t, err)

	email, err := svc.Verify(context.Background(), tok)
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", email)

	_, err = svc.Verify(context.Background(), tok)
	require.Error(t, err, "replay must fail")
}

func TestService_Expiry(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	svc, err := magiclink.New(magiclink.NewMemoryStore(), clk, []byte("k"), 1*time.Minute)
	require.NoError(t, err)

	tok, err := svc.Issue(context.Background(), "bob@example.com")
	require.NoError(t, err)

	clk.Advance(2 * time.Minute)
	_, err = svc.Verify(context.Background(), tok)
	require.Error(t, err)
}

func TestService_RejectsEmptyEmail(t *testing.T) {
	t.Parallel()
	svc, err := magiclink.New(magiclink.NewMemoryStore(), nil, []byte("k"), 0)
	require.NoError(t, err)
	_, err = svc.Issue(context.Background(), "  ")
	require.Error(t, err)
}
