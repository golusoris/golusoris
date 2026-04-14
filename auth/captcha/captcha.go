// Package captcha verifies CAPTCHA tokens against the major providers
// (Cloudflare Turnstile, hCaptcha, Google reCAPTCHA v2/v3).
//
// Each provider has the same wire shape: POST a form with the secret,
// the user's response, and (optionally) the remote IP; receive a JSON
// document with a top-level "success" boolean.
//
// Usage:
//
//	v := captcha.NewTurnstile(secret, nil)
//	if err := v.Verify(ctx, token, remoteIP); err != nil { ... }
package captcha

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	gerr "github.com/golusoris/golusoris/errors"
)

// Endpoints for each shipped provider.
const (
	turnstileURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	hcaptchaURL  = "https://hcaptcha.com/siteverify"
	recaptchaURL = "https://www.google.com/recaptcha/api/siteverify"
)

const defaultTimeout = 10 * time.Second

// Verifier verifies a token from a CAPTCHA provider.
type Verifier interface {
	Verify(ctx context.Context, token, remoteIP string) error
}

// HTTPClient is the subset of *http.Client used by Verifier.
// Apps may inject a retrying / OTel-instrumented client.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// httpVerifier is the shared implementation for all providers.
type httpVerifier struct {
	endpoint string
	secret   string
	client   HTTPClient
}

// NewTurnstile returns a Verifier for Cloudflare Turnstile.
func NewTurnstile(secret string, client HTTPClient) Verifier {
	return newVerifier(turnstileURL, secret, client)
}

// NewHCaptcha returns a Verifier for hCaptcha.
func NewHCaptcha(secret string, client HTTPClient) Verifier {
	return newVerifier(hcaptchaURL, secret, client)
}

// NewRecaptcha returns a Verifier for Google reCAPTCHA (v2 and v3).
// For v3, callers should additionally check the score themselves; this
// verifier only returns success/failure.
func NewRecaptcha(secret string, client HTTPClient) Verifier {
	return newVerifier(recaptchaURL, secret, client)
}

func newVerifier(endpoint, secret string, client HTTPClient) *httpVerifier {
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &httpVerifier{endpoint: endpoint, secret: secret, client: client}
}

type response struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
}

// Verify posts the token to the provider and returns nil on success.
// Failures wrap gerr.CodeUnauthorized and include the provider's
// error-codes list.
func (v *httpVerifier) Verify(ctx context.Context, token, remoteIP string) error {
	if token == "" {
		return gerr.Unauthorized("captcha: missing token")
	}
	form := url.Values{}
	form.Set("secret", v.secret)
	form.Set("response", token)
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("captcha: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("captcha: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if readErr != nil {
		return fmt.Errorf("captcha: read body: %w", readErr)
	}
	var r response
	if jsonErr := json.Unmarshal(body, &r); jsonErr != nil {
		return fmt.Errorf("captcha: decode body: %w", jsonErr)
	}
	if !r.Success {
		return gerr.Unauthorized(fmt.Sprintf("captcha: rejected (%s)", strings.Join(r.ErrorCodes, ",")))
	}
	return nil
}
