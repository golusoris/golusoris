// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package solana_test

import (
	"math"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/web3/solana"
)

func TestNewClient(t *testing.T) {
	t.Parallel()

	t.Run("positive endpoint", func(t *testing.T) {
		t.Parallel()
		c := solana.NewClient("https://api.mainnet-beta.solana.com")
		if c == nil {
			t.Fatal("expected non-nil client")
		}
		if c.Raw() == nil {
			t.Fatal("expected non-nil raw rpc client")
		}
	})

	t.Run("boundary empty endpoint", func(t *testing.T) {
		t.Parallel()
		c := solana.NewClient("")
		if c == nil {
			t.Fatal("expected non-nil client")
		}
		if c.Raw() == nil {
			t.Fatal("expected non-nil raw rpc client")
		}
	})
}

func TestSOLBalance_InvalidAddress(t *testing.T) {
	t.Parallel()

	c := solana.NewClient("https://api.devnet.solana.com")

	t.Run("negative invalid base58 characters", func(t *testing.T) {
		t.Parallel()
		_, err := c.SOLBalance(t.Context(), "invalid-base58-0OIl")
		if err == nil {
			t.Fatal("expected error on invalid address, got nil")
		}
		if !strings.Contains(err.Error(), "solana: parse address") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("boundary empty address", func(t *testing.T) {
		t.Parallel()
		_, err := c.SOLBalance(t.Context(), "")
		if err == nil {
			t.Fatal("expected error on empty address, got nil")
		}
		if !strings.Contains(err.Error(), "solana: parse address") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})
}

func TestNewKey(t *testing.T) {
	t.Parallel()

	k, err := solana.NewKey()
	if err != nil {
		t.Fatalf("NewKey() error = %v", err)
	}
	if k == nil {
		t.Fatal("expected non-nil key")
	}
	pub := k.PublicKey()
	if len(pub) < 32 || len(pub) > 44 {
		t.Fatalf("unexpected public key length %d: %q", len(pub), pub)
	}
	if len(k.PrivateKey()) != 64 {
		t.Fatalf("unexpected private key length: %d", len(k.PrivateKey()))
	}
}

func TestKeyFromBase58(t *testing.T) {
	t.Parallel()

	t.Run("positive roundtrip", func(t *testing.T) {
		t.Parallel()
		k, err := solana.NewKey()
		if err != nil {
			t.Fatalf("NewKey() error = %v", err)
		}
		b58 := k.PrivateKey().String()
		k2, err := solana.KeyFromBase58(b58)
		if err != nil {
			t.Fatalf("KeyFromBase58() error = %v", err)
		}
		if k.PublicKey() != k2.PublicKey() {
			t.Fatalf("public key mismatch: %q vs %q", k.PublicKey(), k2.PublicKey())
		}
		if k.PrivateKey().String() != k2.PrivateKey().String() {
			t.Fatalf("private key mismatch: %q vs %q", k.PrivateKey().String(), k2.PrivateKey().String())
		}
	})

	t.Run("negative invalid base58", func(t *testing.T) {
		t.Parallel()
		_, err := solana.KeyFromBase58("0OIl_invalid_base58")
		if err == nil {
			t.Fatal("expected error on invalid base58 string")
		}
		if !strings.Contains(err.Error(), "solana: parse key") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("boundary empty string", func(t *testing.T) {
		t.Parallel()
		_, err := solana.KeyFromBase58("")
		if err == nil {
			t.Fatal("expected error on empty base58 string")
		}
	})
}

func TestLamportsAndSOLConversions(t *testing.T) {
	t.Parallel()

	t.Run("positive lamports to sol", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name     string
			lamports uint64
			wantSOL  float64
		}{
			{
				name:     "positive one sol",
				lamports: 1_000_000_000,
				wantSOL:  1.0,
			},
			{
				name:     "positive fractional sol",
				lamports: 2_500_000_000,
				wantSOL:  2.5,
			},
			{
				name:     "boundary zero",
				lamports: 0,
				wantSOL:  0.0,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got := solana.LamportsToSOL(tc.lamports)
				if math.Abs(got-tc.wantSOL) > 1e-9 {
					t.Fatalf("LamportsToSOL(%d) = %f, want %f", tc.lamports, got, tc.wantSOL)
				}
			})
		}
	})

	t.Run("positive sol to lamports", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name         string
			sol          float64
			wantLamports uint64
		}{
			{
				name:         "positive one point five sol",
				sol:          1.5,
				wantLamports: 1_500_000_000,
			},
			{
				name:         "boundary zero",
				sol:          0.0,
				wantLamports: 0,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got := solana.SOLToLamports(tc.sol)
				if got != tc.wantLamports {
					t.Fatalf("SOLToLamports(%f) = %d, want %d", tc.sol, got, tc.wantLamports)
				}
			})
		}
	})
}
