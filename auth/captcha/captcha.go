// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/core/validate"
)

// Endpoints for each shipped provider.
const (
	turnstileURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	hcaptchaURL  = "https://hcaptcha.com/siteverify"
	recaptchaURL = "https://www.google.com/recaptcha/api/siteverify"
)

const (
	defaultTimeout          = 10 * time.Second
	maxResponseBodyBytes    = 1 << 16
	maxRecaptchaActionBytes = 256
)

type responsePolicy uint8

const (
	responsePolicyBasic responsePolicy = iota
	responsePolicyRecaptchaV2
	responsePolicyRecaptchaV3
)

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
	endpoint       string
	secret         string
	client         HTTPClient
	policy         responsePolicy
	minimumScore   float64
	expectedAction string
}

// NewTurnstile returns a Verifier for Cloudflare Turnstile.
func NewTurnstile(secret string, client HTTPClient) Verifier {
	return newVerifier(turnstileURL, secret, client)
}

// NewHCaptcha returns a Verifier for hCaptcha.
func NewHCaptcha(secret string, client HTTPClient) Verifier {
	return newVerifier(hcaptchaURL, secret, client)
}

// NewRecaptcha returns a Verifier for Google reCAPTCHA v2. It rejects v3
// responses because accepting success without checking score and action is
// unsafe. Use [NewRecaptchaV3] for v3.
func NewRecaptcha(secret string, client HTTPClient) Verifier {
	verifier := newVerifier(recaptchaURL, secret, client)
	verifier.policy = responsePolicyRecaptchaV2
	return verifier
}

// NewRecaptchaV3 returns a Google reCAPTCHA v3 verifier enforcing minimumScore
// and an exact expectedAction. Action names use Google's alphanumeric, slash,
// and underscore grammar and are limited to 256 bytes.
func NewRecaptchaV3(
	secret string,
	client HTTPClient,
	minimumScore float64,
	expectedAction string,
) (Verifier, error) {
	if math.IsNaN(minimumScore) || math.IsInf(minimumScore, 0) || minimumScore < 0 || minimumScore > 1 {
		return nil, errors.New("captcha: reCAPTCHA v3 minimum score must be finite and within [0, 1]")
	}
	if err := validateRecaptchaAction(expectedAction); err != nil {
		return nil, err
	}
	verifier := newVerifier(recaptchaURL, secret, client)
	verifier.policy = responsePolicyRecaptchaV3
	verifier.minimumScore = minimumScore
	verifier.expectedAction = expectedAction
	return verifier, nil
}

func newVerifier(endpoint, secret string, client HTTPClient) *httpVerifier {
	if validate.IsNil(client) {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &httpVerifier{endpoint: endpoint, secret: secret, client: client}
}

type response struct {
	Success    bool            `json:"success"`
	ErrorCodes []string        `json:"error-codes"`
	Score      json.RawMessage `json:"score"`
	Action     json.RawMessage `json:"action"`
}

// Verify posts the token to the provider and returns nil on success. Provider
// and policy rejection use gerr.CodeUnauthorized; transport and protocol
// failures retain their operational error.
func (v *httpVerifier) Verify(ctx context.Context, token, remoteIP string) error {
	if token == "" {
		return gerr.Unauthorized("captcha: missing token")
	}
	return v.requestVerification(ctx, token, remoteIP)
}

func (v *httpVerifier) requestVerification(
	ctx context.Context,
	token string,
	remoteIP string,
) (err error) {
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
	resp, err := v.client.Do(req) //nolint:bodyclose // closed by the deferred gerr.CloseInto below
	if err != nil {
		return fmt.Errorf("captcha: request: %w", err)
	}
	defer gerr.CloseInto(resp.Body, &err, "captcha: close response body")
	providerResponse, err := decodeProviderResponse(resp)
	if err != nil {
		return err
	}
	if !providerResponse.Success {
		return gerr.Unauthorized(fmt.Sprintf(
			"captcha: rejected (%s)",
			strings.Join(providerResponse.ErrorCodes, ","),
		))
	}
	return v.validateResponse(providerResponse)
}

func decodeProviderResponse(resp *http.Response) (response, error) {
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if readErr != nil {
		return response{}, fmt.Errorf("captcha: read body: %w", readErr)
	}
	if len(body) > maxResponseBodyBytes {
		return response{}, fmt.Errorf("captcha: response body exceeds %d bytes", maxResponseBodyBytes)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return response{}, fmt.Errorf("captcha: HTTP status %d", resp.StatusCode)
	}
	var providerResponse response
	if jsonErr := json.Unmarshal(body, &providerResponse); jsonErr != nil {
		return response{}, fmt.Errorf("captcha: decode body: %w", jsonErr)
	}
	return providerResponse, nil
}

func (v *httpVerifier) validateResponse(providerResponse response) error {
	switch v.policy {
	case responsePolicyRecaptchaV2:
		if providerResponse.Score != nil || providerResponse.Action != nil {
			return gerr.Unauthorized("captcha: reCAPTCHA v3 response requires NewRecaptchaV3")
		}
	case responsePolicyRecaptchaV3:
		return v.validateRecaptchaV3Response(providerResponse)
	case responsePolicyBasic:
		return nil
	}
	return errors.New("captcha: unsupported response policy")
}

func (v *httpVerifier) validateRecaptchaV3Response(providerResponse response) error {
	if providerResponse.Score == nil || providerResponse.Action == nil {
		return gerr.Unauthorized("captcha: reCAPTCHA v3 response missing score or action")
	}
	score, err := decodeRecaptchaScore(providerResponse.Score)
	if err != nil {
		return gerr.Unauthorized("captcha: reCAPTCHA v3 response has invalid score")
	}
	action, err := decodeRecaptchaAction(providerResponse.Action)
	if err != nil || action != v.expectedAction {
		return gerr.Unauthorized("captcha: reCAPTCHA v3 action mismatch")
	}
	if score < v.minimumScore {
		return gerr.Unauthorized("captcha: reCAPTCHA v3 score below threshold")
	}
	return nil
}

func decodeRecaptchaScore(raw json.RawMessage) (float64, error) {
	var score *float64
	if err := json.Unmarshal(raw, &score); err != nil {
		return 0, fmt.Errorf("decode score: %w", err)
	}
	if score == nil || math.IsNaN(*score) || math.IsInf(*score, 0) || *score < 0 || *score > 1 {
		return 0, errors.New("score must be finite and within [0, 1]")
	}
	return *score, nil
}

func decodeRecaptchaAction(raw json.RawMessage) (string, error) {
	var action *string
	if err := json.Unmarshal(raw, &action); err != nil {
		return "", fmt.Errorf("decode action: %w", err)
	}
	if action == nil {
		return "", errors.New("action must be a string")
	}
	return *action, nil
}

func validateRecaptchaAction(action string) error {
	if action == "" {
		return errors.New("captcha: reCAPTCHA v3 expected action is required")
	}
	if len(action) > maxRecaptchaActionBytes {
		return fmt.Errorf("captcha: reCAPTCHA v3 expected action exceeds %d bytes", maxRecaptchaActionBytes)
	}
	for i := range len(action) {
		if validRecaptchaActionByte(action[i]) {
			continue
		}
		return errors.New("captcha: reCAPTCHA v3 expected action contains an invalid character")
	}
	return nil
}

func validRecaptchaActionByte(char byte) bool {
	return isASCIIAlphaNumeric(char) || char == '/' || char == '_'
}

func isASCIIAlphaNumeric(char byte) bool {
	return (char >= 'a' && char <= 'z') ||
		(char >= 'A' && char <= 'Z') ||
		(char >= '0' && char <= '9')
}
