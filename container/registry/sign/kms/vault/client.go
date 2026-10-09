// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package vault

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// maxResponse caps every response body; transit answers are a few KiB.
	maxResponse = 1 << 20
	// maxJWT caps the service account token file.
	maxJWT = 64 << 10
)

// client is the transit HTTP API with token or service account login.
type client struct {
	base    *url.URL
	http    *http.Client
	timeout time.Duration
	role    string
	auth    string
	jwtFile string

	mu    sync.Mutex
	token string
	// loginMu serializes logins, so concurrent denials log in once.
	loginMu sync.Mutex
}

func newClient(base *url.URL, cfg Config) *client {
	rt := cfg.Transport
	if rt == nil {
		rt = http.DefaultTransport.(*http.Transport).Clone()
	}
	return &client{
		base:    base,
		http:    &http.Client{Transport: rt, Timeout: cfg.Timeout},
		timeout: cfg.Timeout,
		role:    cfg.Role,
		auth:    cfg.AuthMount,
		jwtFile: cfg.JWTFile,
		token:   cfg.Token,
	}
}

// apiError is a non-2xx answer with the server's error messages.
type apiError struct {
	status int
	msgs   []string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("status %d: %s", e.status, strings.Join(e.msgs, "; "))
}

// response is the envelope of every answer.
type response struct {
	Data json.RawMessage `json:"data"`
	Auth *struct {
		ClientToken string `json:"client_token"`
	} `json:"auth"`
	Errors []string `json:"errors"`
}

// login exchanges the service account JWT for a token.
func (c *client) login(ctx context.Context) error {
	jwt, err := readJWT(c.jwtFile)
	if err != nil {
		return err
	}
	var r response
	body := map[string]string{"role": c.role, "jwt": jwt}
	if err := c.do(ctx, http.MethodPost, "auth/"+c.auth+"/login", "", body, &r); err != nil {
		return fmt.Errorf("kms/vault: login: %w", err)
	}
	if r.Auth == nil || r.Auth.ClientToken == "" {
		return errors.New("kms/vault: login: no client token in answer")
	}
	c.mu.Lock()
	c.token = r.Auth.ClientToken
	c.mu.Unlock()
	return nil
}

func readJWT(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- operator-configured token path.
	if err != nil {
		return "", fmt.Errorf("kms/vault: login: %w", err)
	}
	b, err := io.ReadAll(io.LimitReader(f, maxJWT+1))
	err = errors.Join(err, f.Close())
	if err != nil {
		return "", fmt.Errorf("kms/vault: login: read %s: %w", path, err)
	}
	jwt := strings.TrimSpace(string(b))
	if jwt == "" || len(b) > maxJWT {
		return "", fmt.Errorf("kms/vault: login: %s: want a JWT of 1..%d bytes", path, maxJWT)
	}
	return jwt, nil
}

// write posts body to path and decodes the data object into out. A denied
// request under role login logs in again and is retried once.
func (c *client) write(ctx context.Context, path string, body, out any) error {
	tok := c.currentToken()
	var r response
	err := c.do(ctx, http.MethodPost, path, tok, body, &r)
	if denied(err) && c.role != "" {
		if lerr := c.relogin(ctx, tok); lerr != nil {
			return errors.Join(err, lerr)
		}
		var retry response
		err = c.do(ctx, http.MethodPost, path, c.currentToken(), body, &retry)
		r = retry
	}
	if err != nil {
		return err
	}
	return decodeData(r.Data, out)
}

// relogin logs in unless another call already replaced the denied token.
func (c *client) relogin(ctx context.Context, denied string) error {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	if c.currentToken() != denied {
		return nil
	}
	return c.login(ctx)
}

func (c *client) currentToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

func denied(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.status == http.StatusForbidden
}

func decodeData(data json.RawMessage, out any) error {
	if len(data) == 0 || string(data) == "null" {
		return errors.New("no data in answer")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode data: %w", err)
	}
	return nil
}

// do sends one request bounded by the client timeout and decodes the
// envelope into r.
func (c *client) do(ctx context.Context, method, path, token string, body any, r *response) (err error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var rd io.Reader = http.NoBody
	if body != nil {
		b, merr := json.Marshal(body)
		if merr != nil {
			return fmt.Errorf("encode: %w", merr)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.JoinPath("v1", path).String(), rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return err
	}
	if len(raw) > maxResponse {
		return fmt.Errorf("answer over %d bytes", maxResponse)
	}
	return decodeResponse(resp.StatusCode, raw, r)
}

func decodeResponse(status int, raw []byte, r *response) error {
	jerr := json.Unmarshal(raw, r)
	// Login, key read and sign all answer 200; anything else is a failure.
	if status != http.StatusOK {
		return &apiError{status: status, msgs: r.Errors}
	}
	if jerr != nil {
		return fmt.Errorf("decode answer: %w", jerr)
	}
	return nil
}

// keyInfo is the subset of transit/keys/:name the signer needs.
type keyInfo struct {
	Type            string                     `json:"type"`
	SupportsSigning bool                       `json:"supports_signing"`
	Derived         bool                       `json:"derived"`
	LatestVersion   int                        `json:"latest_version"`
	Keys            map[string]json.RawMessage `json:"keys"`
}

// pinnedKey is one version of a transit key and its public half.
type pinnedKey struct {
	pub     crypto.PublicKey
	version int
}

// readKey returns version (0 = latest) of key.
func (c *client) readKey(ctx context.Context, mount, key string, version int) (*pinnedKey, error) {
	var r response
	if err := c.do(ctx, http.MethodGet, mount+"/keys/"+key, c.currentToken(), nil, &r); err != nil {
		return nil, fmt.Errorf("kms/vault: read key %s: %w", key, err)
	}
	var info keyInfo
	if err := decodeData(r.Data, &info); err != nil {
		return nil, fmt.Errorf("kms/vault: read key %s: %w", key, err)
	}
	if !info.SupportsSigning || info.Derived {
		return nil, fmt.Errorf("%w: %s is a %s key (signing %t, derived %t)", ErrUnsupportedKey, key, info.Type, info.SupportsSigning, info.Derived)
	}
	if version == 0 {
		version = info.LatestVersion
	}
	entry, ok := info.Keys[strconv.Itoa(version)]
	if !ok {
		return nil, fmt.Errorf("kms/vault: read key %s: version %d not available", key, version)
	}
	var v struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(entry, &v); err != nil {
		return nil, fmt.Errorf("%w: %s version %d: %w", ErrUnsupportedKey, key, version, err)
	}
	pub, err := parsePublicKey(info.Type, v.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %s version %d: %w", ErrUnsupportedKey, key, version, err)
	}
	return &pinnedKey{pub: pub, version: version}, nil
}

// parsePublicKey reads transit's encoding: PKIX PEM for ECDSA and RSA,
// base64 of the raw key for Ed25519.
func parsePublicKey(typ, enc string) (crypto.PublicKey, error) {
	if typ == "ed25519" {
		b, err := base64.StdEncoding.DecodeString(enc)
		if err != nil || len(b) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("ed25519 public key: %d bytes, %w", len(b), err)
		}
		return ed25519.PublicKey(b), nil
	}
	block, _ := pem.Decode([]byte(enc))
	if block == nil {
		return nil, fmt.Errorf("type %q: no PEM public key", typ)
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("type %q: %w", typ, err)
	}
	switch pub.(type) {
	case *ecdsa.PublicKey:
		if strings.HasPrefix(typ, "ecdsa-") {
			return pub, nil
		}
	case *rsa.PublicKey:
		if strings.HasPrefix(typ, "rsa-") {
			return pub, nil
		}
	}
	return nil, fmt.Errorf("type %q: public key is %T", typ, pub)
}
