// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package policy enforces password and credential policies. It bundles
// zxcvbn strength scoring with optional HaveIBeenPwned (HIBP)
// k-anonymity breach checking.
//
// Usage:
//
//	p, err := policy.New(policy.Options{MinScore: 3, MinLength: 12, CheckHIBP: true})
//	if err := p.Validate(ctx, password); err != nil { ... }
package policy

import (
	"context"
	"crypto/sha1" // #nosec G505 -- sha1 is required by the HIBP k-anonymity API.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	zxcvbn "github.com/nbutton23/zxcvbn-go"

	gerr "github.com/golusoris/golusoris/core/errors"
)

const (
	hibpURL        = "https://api.pwnedpasswords.com/range/"
	defaultTimeout = 5 * time.Second
	maxHIBPBytes   = 1 << 20
)

// Options tune the password policy.
type Options struct {
	// MinLength is the minimum password length in Unicode code points (default 12).
	MinLength int
	// MinScore is the minimum zxcvbn score (1–4). Default 3.
	MinScore int
	// DisableStrengthCheck explicitly permits a zero minimum score.
	DisableStrengthCheck bool
	// CheckHIBP enables a HaveIBeenPwned k-anonymity lookup.
	CheckHIBP bool
	// MaxBreachCount: if CheckHIBP and the password appears more times
	// than this in HIBP, it is rejected. Default 0 (any breach rejects).
	MaxBreachCount int
	// HTTPClient is used for HIBP queries. Default has a 5s timeout.
	HTTPClient *http.Client
}

func (o Options) withDefaults() (Options, error) {
	if err := o.validate(); err != nil {
		return Options{}, err
	}
	if o.MinLength == 0 {
		o.MinLength = 12
	}
	if o.MinScore == 0 && !o.DisableStrengthCheck {
		o.MinScore = 3
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: defaultTimeout}
	} else {
		client := *o.HTTPClient
		if client.Timeout <= 0 {
			client.Timeout = defaultTimeout
		}
		o.HTTPClient = &client
	}
	return o, nil
}

func (o Options) validate() error {
	if o.MinLength < 0 {
		return errors.New("policy: MinLength must not be negative")
	}
	if o.MinScore < 0 || o.MinScore > 4 {
		return errors.New("policy: MinScore must be between 0 and 4")
	}
	if o.DisableStrengthCheck && o.MinScore != 0 {
		return errors.New("policy: MinScore and DisableStrengthCheck are mutually exclusive")
	}
	if o.MaxBreachCount < 0 {
		return errors.New("policy: MaxBreachCount must not be negative")
	}
	return nil
}

// Policy validates passwords against a configured ruleset.
type Policy struct {
	opts Options
}

// New returns a validated Policy.
func New(opts Options) (*Policy, error) {
	validated, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}
	return &Policy{opts: validated}, nil
}

// Validate returns nil when password meets every rule. Failures wrap
// gerr.CodeValidation with a human-readable reason.
func (p *Policy) Validate(ctx context.Context, password string, userInputs ...string) error {
	if !utf8.ValidString(password) {
		return gerr.Validation("password: must be valid UTF-8")
	}
	if utf8.RuneCountInString(password) < p.opts.MinLength {
		return gerr.Validation(fmt.Sprintf("password: must be at least %d chars", p.opts.MinLength))
	}
	if p.opts.MinScore > 0 {
		score := zxcvbn.PasswordStrength(password, userInputs).Score
		if score < p.opts.MinScore {
			return gerr.Validation(fmt.Sprintf("password: too weak (zxcvbn score %d, need %d)", score, p.opts.MinScore))
		}
	}
	if p.opts.CheckHIBP {
		count, err := p.hibpCount(ctx, password)
		if err != nil {
			return fmt.Errorf("policy: hibp lookup: %w", err)
		}
		if count > p.opts.MaxBreachCount {
			return gerr.Validation(fmt.Sprintf("password: appears in %d known breaches", count))
		}
	}
	return nil
}

// Score returns the raw zxcvbn score (0–4).
func (p *Policy) Score(password string, userInputs ...string) int {
	return zxcvbn.PasswordStrength(password, userInputs).Score
}

// hibpCount queries the HaveIBeenPwned k-anonymity API and returns the
// number of breaches password appears in (0 if none).
func (p *Policy) hibpCount(ctx context.Context, password string) (count int, err error) {
	sum := sha1.Sum([]byte(password)) // #nosec G401 -- HIBP requires sha1. // nosemgrep: go.lang.security.audit.crypto.use_of_weak_crypto.use-of-sha1 (k-anonymity protocol mandates SHA-1)
	hash := strings.ToUpper(hex.EncodeToString(sum[:]))
	prefix, suffix := hash[:5], hash[5:]

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hibpURL+prefix, http.NoBody)
	if err != nil {
		return 0, fmt.Errorf("hibp: build request: %w", err)
	}
	req.Header.Set("Add-Padding", "true")
	resp, err := p.opts.HTTPClient.Do(req) //nolint:bodyclose // closed by the deferred gerr.CloseInto below
	if err != nil {
		return 0, fmt.Errorf("hibp: request: %w", err)
	}
	defer gerr.CloseInto(resp.Body, &err, "policy: close hibp response body")
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("hibp: status %d", resp.StatusCode)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxHIBPBytes+1))
	if readErr != nil {
		return 0, fmt.Errorf("hibp: read body: %w", readErr)
	}
	if len(body) > maxHIBPBytes {
		return 0, errors.New("hibp: response body too large")
	}
	for line := range strings.SplitSeq(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, suffix+":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		n, parseErr := strconv.Atoi(strings.TrimSpace(parts[1]))
		if parseErr != nil {
			return 0, fmt.Errorf("hibp: parse count: %w", parseErr)
		}
		return n, nil
	}
	return 0, nil
}
