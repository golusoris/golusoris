// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package evm_test

import (
	"math/big"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/web3/evm"
)

func TestDial_Negative(t *testing.T) {
	t.Parallel()

	t.Run("negative invalid url scheme", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		_, err := evm.Dial(ctx, "://invalid-url")
		if err == nil {
			t.Fatal("expected dial error on invalid url")
		}
		if !strings.Contains(err.Error(), "evm: dial") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("negative non-existent ipc socket", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		_, err := evm.Dial(ctx, "ipc:///nonexistent/path/socket.ipc")
		if err == nil {
			t.Fatal("expected error with non-existent ipc socket")
		}
		if !strings.Contains(err.Error(), "evm: dial") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})
}

func TestNewKey(t *testing.T) {
	t.Parallel()

	k, err := evm.NewKey()
	if err != nil {
		t.Fatalf("NewKey() error = %v", err)
	}
	if k == nil {
		t.Fatal("expected non-nil key")
	}
	if k.PrivateKey() == nil {
		t.Fatal("expected non-nil private key")
	}
	addr := k.Address()
	if !strings.HasPrefix(addr, "0x") || len(addr) != 42 {
		t.Fatalf("invalid address format: %q", addr)
	}
}

func TestKeyFromHex(t *testing.T) {
	t.Parallel()

	t.Run("positive known private key", func(t *testing.T) {
		t.Parallel()
		// Secret scalar 1: its address is the standard secp256k1 test vector.
		hexKey := strings.Repeat("0", 63) + "1"
		wantAddr := "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf"

		k, err := evm.KeyFromHex(hexKey)
		if err != nil {
			t.Fatalf("KeyFromHex() error = %v", err)
		}
		if got := k.Address(); got != wantAddr {
			t.Fatalf("k.Address() = %q, want %q", got, wantAddr)
		}
		if k.PrivateKey() == nil {
			t.Fatal("expected non-nil private key")
		}
	})

	t.Run("negative invalid hex", func(t *testing.T) {
		t.Parallel()
		_, err := evm.KeyFromHex("invalid-non-hex-characters-zzzz")
		if err == nil {
			t.Fatal("expected error on invalid hex")
		}
		if !strings.Contains(err.Error(), "evm: parse key") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("negative empty string", func(t *testing.T) {
		t.Parallel()
		_, err := evm.KeyFromHex("")
		if err == nil {
			t.Fatal("expected error on empty key string")
		}
	})
}

func TestWeiToEther(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wei     *big.Int
		wantStr string
	}{
		{
			name:    "positive 1 ether in wei",
			wei:     big.NewInt(1_000_000_000_000_000_000),
			wantStr: "1",
		},
		{
			name:    "positive half ether in wei",
			wei:     big.NewInt(500_000_000_000_000_000),
			wantStr: "0.5",
		},
		{
			name:    "boundary zero wei",
			wei:     big.NewInt(0),
			wantStr: "0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evm.WeiToEther(tc.wei)
			if got.Text('g', 10) != tc.wantStr {
				t.Fatalf("WeiToEther(%v) = %s, want %s", tc.wei, got.Text('g', 10), tc.wantStr)
			}
		})
	}
}

func TestEtherToWei(t *testing.T) {
	t.Parallel()

	t.Run("positive 1.5 ether", func(t *testing.T) {
		t.Parallel()
		got, err := evm.EtherToWei("1.5")
		if err != nil {
			t.Fatalf("EtherToWei() error = %v", err)
		}
		want := big.NewInt(1_500_000_000_000_000_000)
		if got.Cmp(want) != 0 {
			t.Fatalf("EtherToWei('1.5') = %s, want %s", got.String(), want.String())
		}
	})

	t.Run("boundary zero ether", func(t *testing.T) {
		t.Parallel()
		got, err := evm.EtherToWei("0")
		if err != nil {
			t.Fatalf("EtherToWei() error = %v", err)
		}
		if got.Sign() != 0 {
			t.Fatalf("EtherToWei('0') = %s, want 0", got.String())
		}
	})

	t.Run("negative invalid string", func(t *testing.T) {
		t.Parallel()
		_, err := evm.EtherToWei("invalid-number")
		if err == nil {
			t.Fatal("expected error on invalid ether string")
		}
		if !strings.Contains(err.Error(), "evm: parse ether") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})
}
