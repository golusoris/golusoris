// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package crypto_test

import (
	"bytes"
	"testing"

	"github.com/alexedwards/argon2id"

	"github.com/golusoris/golusoris/core/crypto"
)

func TestPasswordRoundtrip(t *testing.T) {
	t.Parallel()
	hash, err := crypto.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, rehash, err := crypto.VerifyPassword("hunter2", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("expected match=true")
	}
	if rehash {
		t.Error("default params should not need rehash")
	}
}

func TestPasswordWrong(t *testing.T) {
	t.Parallel()
	hash, _ := crypto.HashPassword("hunter2")
	ok, _, err := crypto.VerifyPassword("wrong", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Error("expected match=false")
	}
}

func TestSealOpenRoundtrip(t *testing.T) {
	t.Parallel()
	key, err := crypto.RandomBytes(32)
	if err != nil {
		t.Fatal(err)
	}
	pt := []byte("the quick brown fox")
	ct, err := crypto.Seal(key, pt)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(ct, pt) {
		t.Error("ciphertext == plaintext")
	}
	out, err := crypto.Open(key, ct)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(out, pt) {
		t.Errorf("Open returned %q, want %q", out, pt)
	}
}

func TestOpenShortInput(t *testing.T) {
	t.Parallel()
	key, _ := crypto.RandomBytes(32)
	if _, err := crypto.Open(key, []byte{1, 2, 3}); err == nil {
		t.Error("expected error for short ciphertext")
	}
}

func TestHashPasswordWith(t *testing.T) {
	t.Parallel()
	h, err := crypto.HashPasswordWith("secret", crypto.DefaultPasswordParams)
	if err != nil {
		t.Fatalf("HashPasswordWith: %v", err)
	}
	ok, _, err := crypto.VerifyPassword("secret", h)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("expected match=true")
	}
}

// owaspMinimums lists the argon2id minimums of the OWASP Password Storage Cheat Sheet (m in KiB, p=1).
var owaspMinimums = []struct{ memory, iterations uint32 }{
	{memory: 47104, iterations: 1},
	{memory: 19456, iterations: 2},
	{memory: 12288, iterations: 3},
	{memory: 9216, iterations: 4},
	{memory: 7168, iterations: 5},
}

func meetsOWASPMinimum(p *argon2id.Params) bool {
	if p == nil || p.Parallelism < 1 {
		return false
	}
	for _, floor := range owaspMinimums {
		if p.Memory >= floor.memory && p.Iterations >= floor.iterations {
			return true
		}
	}
	return false
}

func TestDefaultPasswordParamsMeetOWASPMinimum(t *testing.T) {
	t.Parallel()
	if !meetsOWASPMinimum(crypto.DefaultPasswordParams) {
		t.Errorf("DefaultPasswordParams %+v does not meet OWASP minimum", crypto.DefaultPasswordParams)
	}
	if crypto.DefaultPasswordParams.SaltLength < 16 {
		t.Errorf("SaltLength = %d, want >= 16", crypto.DefaultPasswordParams.SaltLength)
	}
	if crypto.DefaultPasswordParams.KeyLength < 32 {
		t.Errorf("KeyLength = %d, want >= 32", crypto.DefaultPasswordParams.KeyLength)
	}
}

func TestHashPasswordEncodesDefaultParams(t *testing.T) {
	t.Parallel()
	h, err := crypto.HashPassword("x")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	p, salt, key, err := argon2id.DecodeHash(h)
	if err != nil {
		t.Fatalf("DecodeHash: %v", err)
	}
	if p.Memory != crypto.DefaultPasswordParams.Memory {
		t.Errorf("p.Memory = %d, want %d", p.Memory, crypto.DefaultPasswordParams.Memory)
	}
	if p.Iterations != crypto.DefaultPasswordParams.Iterations {
		t.Errorf("p.Iterations = %d, want %d", p.Iterations, crypto.DefaultPasswordParams.Iterations)
	}
	if p.Parallelism != crypto.DefaultPasswordParams.Parallelism {
		t.Errorf("p.Parallelism = %d, want %d", p.Parallelism, crypto.DefaultPasswordParams.Parallelism)
	}
	if uint32(len(salt)) != crypto.DefaultPasswordParams.SaltLength {
		t.Errorf("len(salt) = %d, want %d", len(salt), crypto.DefaultPasswordParams.SaltLength)
	}
	if uint32(len(key)) != crypto.DefaultPasswordParams.KeyLength {
		t.Errorf("len(key) = %d, want %d", len(key), crypto.DefaultPasswordParams.KeyLength)
	}
	if !meetsOWASPMinimum(p) {
		t.Errorf("decoded params %+v do not meet OWASP minimum", p)
	}
}

func TestOWASPMinimumBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		memory      uint32
		iterations  uint32
		parallelism uint8
		want        bool
	}{
		{name: "19456_2_1", memory: 19456, iterations: 2, parallelism: 1, want: true},
		{name: "19455_2_1", memory: 19455, iterations: 2, parallelism: 1, want: false},
		{name: "47104_1_1", memory: 47104, iterations: 1, parallelism: 1, want: true},
		{name: "47103_1_1", memory: 47103, iterations: 1, parallelism: 1, want: false},
		{name: "7168_5_1", memory: 7168, iterations: 5, parallelism: 1, want: true},
		{name: "7167_5_1", memory: 7167, iterations: 5, parallelism: 1, want: false},
		{name: "65536_3_0", memory: 65536, iterations: 3, parallelism: 0, want: false},
		{name: "1<<20_0_1", memory: 1 << 20, iterations: 0, parallelism: 1, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := &argon2id.Params{
				Memory:      tc.memory,
				Iterations:  tc.iterations,
				Parallelism: tc.parallelism,
			}
			if got := meetsOWASPMinimum(p); got != tc.want {
				t.Errorf("meetsOWASPMinimum(%+v) = %v, want %v", p, got, tc.want)
			}
		})
	}
}
