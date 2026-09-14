// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package receipt_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/crypto/receipt"
)

// 0xab keeps the hex form non-numeric so a YAML reader cannot mistake it for an int.
var fixedSeed = bytes.Repeat([]byte{0xab}, ed25519.SeedSize)

func newSigner(t *testing.T) *receipt.Signer {
	t.Helper()
	s, err := receipt.NewSignerFromSeed(fixedSeed, clock.NewFake())
	if err != nil {
		t.Fatalf("NewSignerFromSeed: %v", err)
	}
	return s
}

func TestSignVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	s := newSigner(t)
	r, err := s.Sign(receipt.Run{Command: "make verify-all", Output: []byte("ok\n"), CommitSHA: "abc123", Repository: "golusoris/golusoris"})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if r.Version != receipt.Version || r.PublicKey != s.PublicKey() {
		t.Fatalf("unexpected receipt header: %+v", r)
	}
	if err = receipt.VerifyOutput(r, []byte("ok\n")); err != nil {
		t.Fatalf("VerifyOutput: %v", err)
	}
	// Receipts survive JSON transport unchanged.
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back receipt.Receipt
	if err = json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if err = receipt.Verify(&back); err != nil {
		t.Fatalf("Verify after JSON round trip: %v", err)
	}
}

func TestSignIsDeterministicUnderFakeClock(t *testing.T) {
	t.Parallel()
	run := receipt.Run{Command: "go test ./..."}
	at := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	s1, err := receipt.NewSignerFromSeed(fixedSeed, clockwork.NewFakeClockAt(at))
	if err != nil {
		t.Fatal(err)
	}
	s2, err := receipt.NewSignerFromSeed(fixedSeed, clockwork.NewFakeClockAt(at))
	if err != nil {
		t.Fatal(err)
	}
	a, err := s1.Sign(run)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s2.Sign(run)
	if err != nil {
		t.Fatal(err)
	}
	if a.Signature != b.Signature {
		t.Fatal("same seed + same clock must produce identical signatures")
	}
}

func TestSignRejects(t *testing.T) {
	t.Parallel()
	s := newSigner(t)
	tests := []struct {
		name string
		run  receipt.Run
		want error
	}{
		{name: "non-zero exit", run: receipt.Run{Command: "x", ExitCode: 1}, want: receipt.ErrNonZeroExit},
		{name: "empty command", run: receipt.Run{Command: "  "}, want: receipt.ErrEmptyCommand},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := s.Sign(tc.run); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	t.Parallel()
	good, err := newSigner(t).Sign(receipt.Run{Command: "make ci", Output: []byte("pass")})
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(f func(r *receipt.Receipt)) *receipt.Receipt {
		c := *good
		f(&c)
		return &c
	}
	tests := []struct {
		name string
		r    *receipt.Receipt
		want error
	}{
		{name: "nil", r: nil, want: receipt.ErrNilReceipt},
		{name: "command changed", r: mutate(func(r *receipt.Receipt) { r.Command = "true" }), want: receipt.ErrSignature},
		{name: "timestamp changed", r: mutate(func(r *receipt.Receipt) { r.Timestamp = r.Timestamp.Add(time.Second) }), want: receipt.ErrSignature},
		{name: "exit code forged", r: mutate(func(r *receipt.Receipt) { r.ExitCode = 2 }), want: receipt.ErrNonZeroExit},
		{name: "bad public key hex", r: mutate(func(r *receipt.Receipt) { r.PublicKey = "zz" }), want: receipt.ErrBadPublicKey},
		{name: "short signature", r: mutate(func(r *receipt.Receipt) { r.Signature = "abcd" }), want: receipt.ErrBadSignature},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := receipt.Verify(tc.r); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	if err := receipt.VerifyOutput(good, []byte("fail")); !errors.Is(err, receipt.ErrOutputHash) {
		t.Fatalf("got %v want ErrOutputHash", err)
	}
}

func TestConstructorsValidateKeys(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	if _, err := receipt.NewSigner(make([]byte, 5), clk); !errors.Is(err, receipt.ErrBadKey) {
		t.Fatalf("short private key: got %v", err)
	}
	if _, err := receipt.NewSignerFromSeed(make([]byte, 31), clk); !errors.Is(err, receipt.ErrBadKey) {
		t.Fatalf("short seed: got %v", err)
	}
	if _, err := receipt.NewSignerFromSeed(fixedSeed, nil); !errors.Is(err, receipt.ErrNilClock) {
		t.Fatalf("nil clock: got %v", err)
	}
	_, priv, err := receipt.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receipt.NewSigner(priv, clk); err != nil {
		t.Fatalf("generated key rejected: %v", err)
	}
}

// Payload format is the interoperability contract with praetor; pin it.
func TestPayloadFormat(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 9, 14, 12, 0, 0, 5, time.UTC)
	r := &receipt.Receipt{Version: "v1", Command: "c", ExitCode: 0, Timestamp: ts, CommitSHA: "sha", Repository: "o/r", OutputHash: "h"}
	want := "v1\nc\n0\n2026-09-14T12:00:00.000000005Z\nsha\no/r\nh"
	if got := string(r.Payload()); got != want {
		t.Fatalf("payload\n got %q\nwant %q", got, want)
	}
}
