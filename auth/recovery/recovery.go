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
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jonboulle/clockwork"

	gerr "github.com/golusoris/golusoris/errors"
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
	FindForUser(ctx context.Context, userID string) ([]Code, error)
	MarkUsed(ctx context.Context, userID string, hash []byte) error
}

// TokenStore persists reset-token records.
type TokenStore interface {
	Save(ctx context.Context, t Token) error
	Find(ctx context.Context, hash []byte) (Token, error)
	MarkUsed(ctx context.Context, hash []byte) error
}

// Service issues + validates recovery codes and reset tokens.
type Service struct {
	codes  CodeStore
	tokens TokenStore
	clk    clockwork.Clock
	secret []byte
}

// New returns a Service. secret is the HMAC key used to hash codes and
// tokens before storage. Either store may be nil if the corresponding
// flow is not used.
func New(codes CodeStore, tokens TokenStore, clk clockwork.Clock, secret []byte) *Service {
	if clk == nil {
		clk = clockwork.NewRealClock()
	}
	if len(secret) == 0 {
		panic("recovery: secret must not be empty")
	}
	return &Service{codes: codes, tokens: tokens, clk: clk, secret: secret}
}

// IssueCodes generates n one-time recovery codes for userID. Returns the
// raw codes (show once) and persists their HMAC.
func (s *Service) IssueCodes(ctx context.Context, userID string, n int) ([]string, error) {
	if s.codes == nil {
		return nil, errors.New("recovery: no code store configured")
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
		records[i] = Code{UserID: userID, Hash: s.hash(raw)}
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
	hash := s.hash(raw)
	all, err := s.codes.FindForUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("recovery: lookup: %w", err)
	}
	for _, c := range all {
		if c.UsedAt != nil {
			continue
		}
		if hmac.Equal(c.Hash, hash) {
			if markErr := s.codes.MarkUsed(ctx, userID, hash); markErr != nil {
				return fmt.Errorf("recovery: mark used: %w", markErr)
			}
			return nil
		}
	}
	return gerr.Unauthorized("recovery: invalid code")
}

// IssueResetToken creates a single-use reset token valid for ttl.
// Returns the raw token (deliver out-of-band, e.g. by email).
func (s *Service) IssueResetToken(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	if s.tokens == nil {
		return "", errors.New("recovery: no token store configured")
	}
	raw, err := randomToken()
	if err != nil {
		return "", err
	}
	hash := s.hash(raw)
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
	hash := s.hash(raw)
	t, err := s.tokens.Find(ctx, hash)
	if err != nil {
		return "", fmt.Errorf("%w: recovery: find: %w", gerr.Unauthorized("invalid reset token"), err)
	}
	if t.UsedAt != nil {
		return "", gerr.Unauthorized("reset token already used")
	}
	if s.clk.Now().After(t.ExpiresAt) {
		return "", gerr.Unauthorized("reset token expired")
	}
	if useErr := s.tokens.MarkUsed(ctx, hash); useErr != nil {
		return "", fmt.Errorf("recovery: mark used: %w", useErr)
	}
	return t.UserID, nil
}

func (s *Service) hash(raw string) []byte {
	h := hmac.New(sha256.New, s.secret)
	h.Write([]byte(raw))
	return h.Sum(nil)
}

// randomCode returns a 13-char base32 (uppercase, no padding) code.
func randomCode() (string, error) {
	b := make([]byte, codeBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("recovery: rand: %w", err)
	}
	return strings.TrimRight(base64.RawURLEncoding.EncodeToString(b), "="), nil
}

func randomToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("recovery: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
