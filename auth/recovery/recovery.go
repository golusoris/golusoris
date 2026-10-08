// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package recovery provides backup recovery codes and password-reset
// tokens. Codes are HMAC-hashed before storage; the raw value is shown
// once at issuance time.
//
// Two flows are supported:
//
//   - Recovery codes — N single-use codes the user prints/saves; useful
//     when MFA is lost.
//   - Reset tokens — short-lived tokens emailed during a password-reset
//     flow.
//
// Storage is pluggable via [CodeStore] (recovery codes) and
// [TokenStore] (reset tokens).
package recovery

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jonboulle/clockwork"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/core/validate"
	tokenhash "github.com/golusoris/golusoris/hash"
)

const (
	codeBytes  = 8 // 8 random bytes → ~13-char base32 chunks
	tokenBytes = 24
)

// Code is the metadata for a stored recovery code.
type Code struct {
	UserID string
	Hash   []byte
	UsedAt *time.Time
}

// Token is the metadata for a stored reset token.
type Token struct {
	UserID    string
	Hash      []byte
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// CodeStore persists recovery-code records.
type CodeStore interface {
	SaveBatch(ctx context.Context, codes []Code) error
	// Consume atomically returns and marks one matching unused code as used.
	// Missing and already-used codes return an error.
	Consume(ctx context.Context, userID string, hash []byte) (Code, error)
}

// TokenStore persists reset-token records.
type TokenStore interface {
	Save(ctx context.Context, t Token) error
	// Consume atomically returns and marks one matching unused token as used.
	// Missing and already-used tokens return an error.
	Consume(ctx context.Context, hash []byte) (Token, error)
}

// Service issues + validates recovery codes and reset tokens.
type Service struct {
	codes  CodeStore
	tokens TokenStore
	clk    clockwork.Clock
	secret []byte
}

// New returns a Service. secret is the HMAC key used to hash codes and
// tokens before storage; an empty secret is an error. Either store may
// be nil if the corresponding flow is not used, but at least one store
// must be configured.
func New(codes CodeStore, tokens TokenStore, clk clockwork.Clock, secret []byte) (*Service, error) {
	if validate.IsNil(codes) {
		codes = nil
	}
	if validate.IsNil(tokens) {
		tokens = nil
	}
	if codes == nil && tokens == nil {
		return nil, errors.New("recovery: at least one store must be configured")
	}
	if err := tokenhash.ValidateHMACSHA256Key(secret); err != nil {
		return nil, fmt.Errorf("recovery: secret: %w", err)
	}
	if validate.IsNil(clk) {
		clk = clockwork.NewRealClock()
	}
	return &Service{
		codes:  codes,
		tokens: tokens,
		clk:    clk,
		secret: append([]byte(nil), secret...),
	}, nil
}

// IssueCodes generates n one-time recovery codes for userID. Returns the
// raw codes (show once) and persists their HMAC.
func (s *Service) IssueCodes(ctx context.Context, userID string, n int) ([]string, error) {
	if s.codes == nil {
		return nil, errors.New("recovery: no code store configured")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, gerr.Validation("recovery: userID required")
	}
	if n < 1 || n > 32 {
		return nil, gerr.Validation("recovery: n must be 1..32")
	}
	raws := make([]string, n)
	records := make([]Code, n)
	for i := range n {
		raw, err := randomCode()
		if err != nil {
			return nil, err
		}
		raws[i] = raw
		records[i] = Code{UserID: userID, Hash: tokenhash.HMACSHA256(s.secret, []byte(raw))}
	}
	if err := s.codes.SaveBatch(ctx, records); err != nil {
		return nil, fmt.Errorf("recovery: save codes: %w", err)
	}
	return raws, nil
}

// VerifyCode consumes a recovery code; returns gerr.CodeUnauthorized on
// any failure. Each code can be used at most once.
func (s *Service) VerifyCode(ctx context.Context, userID, raw string) error {
	if s.codes == nil {
		return errors.New("recovery: no code store configured")
	}
	hash := tokenhash.HMACSHA256(s.secret, []byte(raw))
	code, err := s.codes.Consume(ctx, userID, hash)
	if err != nil {
		return fmt.Errorf("%w: recovery: consume code: %w", gerr.Unauthorized("recovery: invalid code"), err)
	}
	if !hmac.Equal(code.Hash, hash) {
		return gerr.Unauthorized("recovery: code mismatch")
	}
	return nil
}

// IssueResetToken creates a single-use reset token valid for ttl.
// Returns the raw token (deliver out-of-band, e.g. by email).
func (s *Service) IssueResetToken(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	if s.tokens == nil {
		return "", errors.New("recovery: no token store configured")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", gerr.Validation("recovery: userID required")
	}
	if ttl <= 0 {
		return "", gerr.Validation("recovery: ttl must be positive")
	}
	raw, err := randomToken()
	if err != nil {
		return "", err
	}
	hash := tokenhash.HMACSHA256(s.secret, []byte(raw))
	t := Token{
		UserID:    userID,
		Hash:      hash,
		ExpiresAt: s.clk.Now().Add(ttl),
	}
	if saveErr := s.tokens.Save(ctx, t); saveErr != nil {
		return "", fmt.Errorf("recovery: save token: %w", saveErr)
	}
	return raw, nil
}

// VerifyResetToken consumes a reset token and returns the userID it was
// issued for. Failures wrap gerr.CodeUnauthorized.
func (s *Service) VerifyResetToken(ctx context.Context, raw string) (string, error) {
	if s.tokens == nil {
		return "", errors.New("recovery: no token store configured")
	}
	hash := tokenhash.HMACSHA256(s.secret, []byte(raw))
	t, err := s.tokens.Consume(ctx, hash)
	if err != nil {
		return "", fmt.Errorf("%w: recovery: consume token: %w", gerr.Unauthorized("invalid reset token"), err)
	}
	if !s.clk.Now().Before(t.ExpiresAt) {
		return "", gerr.Unauthorized("reset token expired")
	}
	if !hmac.Equal(t.Hash, hash) {
		return "", gerr.Unauthorized("reset token mismatch")
	}
	return t.UserID, nil
}

// randomCode returns a 13-char base32 (uppercase, no padding) code.
func randomCode() (string, error) {
	b := make([]byte, codeBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("recovery: rand: %w", err)
	}
	return strings.TrimRight(base32.StdEncoding.EncodeToString(b), "="), nil
}

func randomToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("recovery: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
