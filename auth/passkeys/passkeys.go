// Package passkeys wraps go-webauthn/webauthn for passkey
// (registration + assertion) and pquerna/otp for TOTP MFA.
//
// Usage (registration):
//
//	svc, _ := passkeys.New(passkeys.Options{RPID: "example.com", RPName: "Example", RPOrigins: []string{"https://example.com"}})
//	opts, sess, _ := svc.BeginRegistration(user)
//	// → send opts to browser, store sess in session
//	// browser returns AttestationResponse:
//	cred, _ := svc.FinishRegistration(user, sess, request)
//	// → persist cred via your CredentialStore
//
// Usage (login): BeginLogin / FinishLogin mirror the registration flow.
//
// TOTP is independent: Provision returns an otpauth:// URL; Verify
// checks a 6-digit code.
package passkeys

import (
	"encoding/base32"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// Options configure the WebAuthn relying party.
type Options struct {
	// RPID is the relying-party ID — typically the eTLD+1 of the origin
	// (e.g. "example.com").
	RPID string
	// RPName is the human-readable name shown by browsers.
	RPName string
	// RPOrigins are the allowed origins (e.g. "https://example.com").
	RPOrigins []string
}

// Service is a thin wrapper around webauthn.WebAuthn.
type Service struct{ wa *webauthn.WebAuthn }

// New constructs a Service.
func New(opts Options) (*Service, error) {
	cfg := &webauthn.Config{
		RPID:          opts.RPID,
		RPDisplayName: opts.RPName,
		RPOrigins:     opts.RPOrigins,
	}
	wa, err := webauthn.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("passkeys: configure webauthn: %w", err)
	}
	return &Service{wa: wa}, nil
}

// User is the contract WebAuthn requires for the user being registered
// or authenticated. Apps adapt their user model to this interface.
type User = webauthn.User

// Credential is the persisted credential record.
type Credential = webauthn.Credential

// SessionData is the per-flow state that must round-trip via the
// server-side session.
type SessionData = webauthn.SessionData

// BeginRegistration starts a new credential registration.
func (s *Service) BeginRegistration(u User) (*protocol.CredentialCreation, *SessionData, error) {
	c, sess, err := s.wa.BeginRegistration(u)
	if err != nil {
		return nil, nil, fmt.Errorf("passkeys: begin register: %w", err)
	}
	return c, sess, nil
}

// FinishRegistration validates the browser's response and returns the
// credential to persist.
func (s *Service) FinishRegistration(u User, sess SessionData, r *http.Request) (*Credential, error) {
	cred, err := s.wa.FinishRegistration(u, sess, r)
	if err != nil {
		return nil, fmt.Errorf("passkeys: finish register: %w", err)
	}
	return cred, nil
}

// BeginLogin starts an assertion (login) flow.
func (s *Service) BeginLogin(u User) (*protocol.CredentialAssertion, *SessionData, error) {
	c, sess, err := s.wa.BeginLogin(u)
	if err != nil {
		return nil, nil, fmt.Errorf("passkeys: begin login: %w", err)
	}
	return c, sess, nil
}

// FinishLogin validates the browser's assertion.
func (s *Service) FinishLogin(u User, sess SessionData, r *http.Request) (*Credential, error) {
	cred, err := s.wa.FinishLogin(u, sess, r)
	if err != nil {
		return nil, fmt.Errorf("passkeys: finish login: %w", err)
	}
	return cred, nil
}

// --- TOTP (MFA) ---

// ProvisionTOTP generates a new TOTP secret + otpauth:// URL for the
// given account (e.g. user's email). Show the URL as a QR code to the
// user; persist key.Secret() server-side.
func ProvisionTOTP(issuer, account string) (*otp.Key, error) {
	k, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: account,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return nil, fmt.Errorf("passkeys: provision totp: %w", err)
	}
	return k, nil
}

// VerifyTOTP checks a 6-digit code against secret using the wall clock.
// Accepts ±1 period (30s) of clock skew. Prefer [VerifyTOTPAt] in
// non-handler code so the time source is explicit and testable.
func VerifyTOTP(secret, code string) error {
	return VerifyTOTPAt(secret, code, time.Now()) //nolint:forbidigo // wall-clock wrapper around VerifyTOTPAt
}

// VerifyTOTPAt checks a 6-digit code against secret as of at. Accepts
// ±1 period (30s) of clock skew.
func VerifyTOTPAt(secret, code string, at time.Time) error {
	// Validate that secret is base32 (otp expects this).
	if _, err := base32.StdEncoding.DecodeString(secret); err != nil {
		return errors.New("passkeys: secret is not base32")
	}
	ok, err := totp.ValidateCustom(code, secret, at, totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		return fmt.Errorf("passkeys: validate totp: %w", err)
	}
	if !ok {
		return errors.New("passkeys: invalid totp code")
	}
	return nil
}
