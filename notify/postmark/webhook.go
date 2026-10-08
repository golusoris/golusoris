// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package postmark

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
)

var (
	// ErrWebhookUnauthorized reports missing or mismatched webhook credentials.
	ErrWebhookUnauthorized  = errors.New("notify/postmark: webhook authentication failed")
	errWebhookUsernameEmpty = errors.New("notify/postmark: webhook username is required")
	errWebhookPasswordEmpty = errors.New("notify/postmark: webhook password is required")
)

// WebhookVerifier authenticates a Postmark webhook request and its bounded raw
// body before provider payload parsing, acknowledgement, or dispatch.
type WebhookVerifier func(r *http.Request, body []byte) error

// NewBasicAuthVerifier returns a constant-time HTTP Basic Auth verifier.
// Postmark does not sign webhook payloads; configure matching webhook
// credentials in Postmark and restrict source IPs at the deployment edge.
func NewBasicAuthVerifier(username, password string) (WebhookVerifier, error) {
	if username == "" {
		return nil, errWebhookUsernameEmpty
	}
	if password == "" {
		return nil, errWebhookPasswordEmpty
	}
	wantUsername := sha256.Sum256([]byte(username))
	wantPassword := sha256.Sum256([]byte(password))
	return func(r *http.Request, _ []byte) error {
		gotUsername, gotPassword, ok := r.BasicAuth()
		gotUsernameSum := sha256.Sum256([]byte(gotUsername))
		gotPasswordSum := sha256.Sum256([]byte(gotPassword))
		usernameMatches := subtle.ConstantTimeCompare(gotUsernameSum[:], wantUsername[:])
		passwordMatches := subtle.ConstantTimeCompare(gotPasswordSum[:], wantPassword[:])
		if !ok || usernameMatches&passwordMatches != 1 {
			return ErrWebhookUnauthorized
		}
		return nil
	}, nil
}
