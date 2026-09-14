// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package receipt issues and verifies Ed25519-signed execution receipts — a
// compact, portable proof that a named command exited 0 at a given time for a
// given commit, with a SHA-256 digest of its output. Governance tooling
// (praetor HISS-16 "Exit-0 receipt") attaches one to every passing
// verification run so a reviewer — human or bot — can check that the gate was
// run rather than asserted. The canonical payload is wire-compatible with
// praetor lockdown.ExecutionReceipt.
package receipt

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golusoris/golusoris/core/clock"
)

// Version is the receipt schema version written into every receipt.
const Version = "v1"

// Sentinel errors. Compare with errors.Is.
var (
	ErrNonZeroExit  = errors.New("receipt: refusing to sign a non-zero exit")
	ErrEmptyCommand = errors.New("receipt: command must not be empty")
	ErrNilReceipt   = errors.New("receipt: nil receipt")
	ErrBadKey       = errors.New("receipt: invalid Ed25519 key")
	ErrNilClock     = errors.New("receipt: clock must not be nil")
	ErrBadPublicKey = errors.New("receipt: malformed public key")
	ErrBadSignature = errors.New("receipt: malformed signature")
	ErrSignature    = errors.New("receipt: signature verification failed")
	ErrOutputHash   = errors.New("receipt: output hash mismatch")
)

// Receipt is the signed attestation. Field order is the canonical payload
// order; JSON names are wire-stable.
type Receipt struct {
	Version    string    `json:"version" yaml:"version"`
	Command    string    `json:"command" yaml:"command"`
	ExitCode   int       `json:"exit_code" yaml:"exit_code"`
	Timestamp  time.Time `json:"timestamp" yaml:"timestamp"`
	CommitSHA  string    `json:"commit_sha,omitempty" yaml:"commit_sha,omitempty"`
	Repository string    `json:"repository,omitempty" yaml:"repository,omitempty"`
	OutputHash string    `json:"output_hash" yaml:"output_hash"`
	PublicKey  string    `json:"public_key" yaml:"public_key"`
	Signature  string    `json:"signature" yaml:"signature"`
}

// Run describes the execution being attested.
type Run struct {
	Command    string
	ExitCode   int
	Output     []byte
	CommitSHA  string
	Repository string
}

// Signer signs receipts with a fixed Ed25519 key. Time comes from the
// injected clock so receipts are reproducible in tests.
type Signer struct {
	priv ed25519.PrivateKey
	clk  clock.Clock
}

// GenerateKey returns a fresh Ed25519 key pair from crypto/rand.
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("receipt: generate key: %w", err)
	}
	return pub, priv, nil
}

// NewSigner wraps an Ed25519 private key.
func NewSigner(priv ed25519.PrivateKey, clk clock.Clock) (*Signer, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: private key is %d bytes, want %d", ErrBadKey, len(priv), ed25519.PrivateKeySize)
	}
	if clk == nil {
		return nil, ErrNilClock
	}
	return &Signer{priv: priv, clk: clk}, nil
}

// NewSignerFromSeed derives the key pair from a 32-byte seed — the form that
// fits a config value or secret store entry.
func NewSignerFromSeed(seed []byte, clk clock.Clock) (*Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%w: seed is %d bytes, want %d", ErrBadKey, len(seed), ed25519.SeedSize)
	}
	return NewSigner(ed25519.NewKeyFromSeed(seed), clk)
}

// PublicKey returns the hex-encoded verifying key.
func (s *Signer) PublicKey() string {
	pub, _ := s.priv.Public().(ed25519.PublicKey) // always ed25519 for an ed25519 private key
	return hex.EncodeToString(pub)
}

// Sign attests run. It refuses non-zero exits: a receipt is proof of success,
// never a record of failure.
func (s *Signer) Sign(run Run) (*Receipt, error) {
	if run.ExitCode != 0 {
		return nil, fmt.Errorf("%w: exit code %d", ErrNonZeroExit, run.ExitCode)
	}
	if strings.TrimSpace(run.Command) == "" {
		return nil, ErrEmptyCommand
	}
	sum := sha256.Sum256(run.Output)
	r := &Receipt{
		Version:    Version,
		Command:    run.Command,
		ExitCode:   0,
		Timestamp:  s.clk.Now().UTC(),
		CommitSHA:  run.CommitSHA,
		Repository: run.Repository,
		OutputHash: hex.EncodeToString(sum[:]),
		PublicKey:  s.PublicKey(),
	}
	r.Signature = hex.EncodeToString(ed25519.Sign(s.priv, r.Payload()))
	return r, nil
}

// Payload is the canonical byte sequence that is signed: the wire fields
// joined by "\n" in struct order, the timestamp as RFC 3339 nano in UTC.
func (r *Receipt) Payload() []byte {
	return []byte(strings.Join([]string{
		r.Version,
		r.Command,
		strconv.Itoa(r.ExitCode),
		r.Timestamp.UTC().Format(time.RFC3339Nano),
		r.CommitSHA,
		r.Repository,
		r.OutputHash,
	}, "\n"))
}

// Verify checks the signature over the canonical payload and that the receipt
// records a zero exit.
func Verify(r *Receipt) error {
	if r == nil {
		return ErrNilReceipt
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("%w: recorded exit code %d", ErrNonZeroExit, r.ExitCode)
	}
	pub, err := hex.DecodeString(r.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return ErrBadPublicKey
	}
	sig, err := hex.DecodeString(r.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrBadSignature
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), r.Payload(), sig) {
		return ErrSignature
	}
	return nil
}

// VerifyOutput verifies the signature and that output hashes to OutputHash.
func VerifyOutput(r *Receipt, output []byte) error {
	if err := Verify(r); err != nil {
		return err
	}
	sum := sha256.Sum256(output)
	if got := hex.EncodeToString(sum[:]); got != r.OutputHash {
		return fmt.Errorf("%w: want %s got %s", ErrOutputHash, r.OutputHash, got)
	}
	return nil
}
