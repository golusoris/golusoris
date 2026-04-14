// Package policy enforces password and credential policies. It bundles
// zxcvbn strength scoring with optional HaveIBeenPwned (HIBP)
// k-anonymity breach checking.
//
// Usage:
//
//	p := policy.New(policy.Options{MinScore: 3, MinLength: 12, CheckHIBP: true})
//	if err := p.Validate(ctx, password); err != nil { ... }
package policy

import (
	"context"
	"crypto/sha1" //nolint:gosec // sha1 is required by the HIBP k-anonymity API.
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	zxcvbn "github.com/nbutton23/zxcvbn-go"

	gerr "github.com/golusoris/golusoris/errors"
)

const (
	hibpURL        = "https://api.pwnedpasswords.com/range/"
	defaultTimeout = 5 * time.Second
)

// Options tune the password policy.
type Options struct {
	// MinLength is the minimum password length (default 12).
	MinLength int
	// MinScore is the minimum zxcvbn score (0–4). Default 3.
	MinScore int
	// CheckHIBP enables a HaveIBeenPwned k-anonymity lookup.
	CheckHIBP bool
	// MaxBreachCount: if CheckHIBP and the password appears more times
	// than this in HIBP, it is rejected. Default 0 (any breach rejects).
	MaxBreachCount int
	// HTTPClient is used for HIBP queries. Default has a 5s timeout.
	HTTPClient *http.Client
}

func (o Options) withDefaults() Options {
	if o.MinLength == 0 {
		o.MinLength = 12
	}
	if o.MinScore == 0 {
		o.MinScore = 3
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: defaultTimeout}
	}
	return o
}

// Policy validates passwords against a configured ruleset.
type Policy struct {
	opts Options
}

// New returns a Policy.
func New(opts Options) *Policy { return &Policy{opts: opts.withDefaults()} }

// Validate returns nil when password meets every rule. Failures wrap
// gerr.CodeValidation with a human-readable reason.
func (p *Policy) Validate(ctx context.Context, password string, userInputs ...string) error {
	if len(password) < p.opts.MinLength {
		return gerr.Validation(fmt.Sprintf("password: must be at least %d chars", p.opts.MinLength))
	}
	score := zxcvbn.PasswordStrength(password, userInputs).Score
	if score < p.opts.MinScore {
		return gerr.Validation(fmt.Sprintf("password: too weak (zxcvbn score %d, need %d)", score, p.opts.MinScore))
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
func (p *Policy) hibpCount(ctx context.Context, password string) (int, error) {
	sum := sha1.Sum([]byte(password)) //nolint:gosec // HIBP requires sha1.
	hash := strings.ToUpper(hex.EncodeToString(sum[:]))
	prefix, suffix := hash[:5], hash[5:]

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hibpURL+prefix, http.NoBody)
	if err != nil {
		return 0, fmt.Errorf("hibp: build request: %w", err)
	}
	req.Header.Set("Add-Padding", "true")
	resp, err := p.opts.HTTPClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("hibp: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("hibp: status %d", resp.StatusCode)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return 0, fmt.Errorf("hibp: read body: %w", readErr)
	}
	for _, line := range strings.Split(string(body), "\n") {
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
