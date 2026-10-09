// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcp

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
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// maxResponse caps every response body; Cloud KMS answers are a few KiB.
const maxResponse = 1 << 20

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// checksum is the CRC32C of b in the int64-as-string form of the REST API.
func checksum(b []byte) string {
	return strconv.FormatUint(uint64(crc32.Checksum(b, castagnoli)), 10)
}

// client is the Cloud KMS REST API with OAuth2 bearer tokens.
type client struct {
	base    *url.URL
	http    *http.Client
	token   func(context.Context) (*oauth2.Token, error)
	timeout time.Duration
}

// apiError is a non-2xx answer with Google's error status and message.
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("status %d %s: %s", e.status, e.code, e.message)
}

// publicKey reads the key version's public key and checks its algorithm,
// name and checksum.
func (c *client) publicKey(ctx context.Context, key string) (string, crypto.PublicKey, error) {
	var out struct {
		PEM       string `json:"pem"`
		PEMCrc32c string `json:"pemCrc32c"`
		Algorithm string `json:"algorithm"`
		Name      string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, key+"/publicKey", nil, &out); err != nil {
		return "", nil, fmt.Errorf("kms/gcp: get public key %s: %w", key, err)
	}
	if out.Name != key {
		return "", nil, fmt.Errorf("kms/gcp: get public key %s: answer names %q", key, out.Name)
	}
	if out.PEMCrc32c != checksum([]byte(out.PEM)) {
		return "", nil, fmt.Errorf("%w: get public key %s: pemCrc32c %q", ErrChecksum, key, out.PEMCrc32c)
	}
	pub, err := parsePublicKey(out.Algorithm, out.PEM)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %s: %w", ErrUnsupportedKey, key, err)
	}
	return out.Algorithm, pub, nil
}

// parsePublicKey reads the PKIX PEM key and checks it against the
// algorithm's key family, curve or size.
func parsePublicKey(alg, enc string) (crypto.PublicKey, error) {
	a, ok := algorithms[alg]
	if !ok {
		return nil, fmt.Errorf("algorithm %q", alg)
	}
	block, _ := pem.Decode([]byte(enc))
	if block == nil {
		return nil, fmt.Errorf("algorithm %q: no PEM public key", alg)
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("algorithm %q: %w", alg, err)
	}
	match := false
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		match = a.curve == k.Curve
	case *rsa.PublicKey:
		match = a.bits == k.N.BitLen()
	case ed25519.PublicKey:
		match = a.hash == crypto.Hash(0)
	}
	if !match {
		return nil, fmt.Errorf("algorithm %q: public key is %T", alg, pub)
	}
	return pub, nil
}

// sign posts body to asymmetricSign and checks the key version name and
// the CRC32C checksums of the answer.
func (c *client) sign(ctx context.Context, key string, body map[string]any, data bool) ([]byte, error) {
	var out struct {
		Signature            string `json:"signature"`
		SignatureCrc32c      string `json:"signatureCrc32c"`
		VerifiedDigestCrc32c bool   `json:"verifiedDigestCrc32c"`
		VerifiedDataCrc32c   bool   `json:"verifiedDataCrc32c"`
		Name                 string `json:"name"`
	}
	if err := c.do(ctx, http.MethodPost, key+":asymmetricSign", body, &out); err != nil {
		return nil, fmt.Errorf("kms/gcp: sign: %w", err)
	}
	if out.Name != key {
		return nil, fmt.Errorf("kms/gcp: sign: answer names %q, want %s", out.Name, key)
	}
	if verified := out.VerifiedDigestCrc32c && !data || out.VerifiedDataCrc32c && data; !verified {
		return nil, fmt.Errorf("%w: sign: Cloud KMS did not verify the request checksum", ErrChecksum)
	}
	sig, err := base64.StdEncoding.DecodeString(out.Signature)
	if err != nil {
		return nil, fmt.Errorf("kms/gcp: sign: decode signature: %w", err)
	}
	if out.SignatureCrc32c != checksum(sig) {
		return nil, fmt.Errorf("%w: sign: signatureCrc32c %q", ErrChecksum, out.SignatureCrc32c)
	}
	return sig, nil
}

// do sends one authorized request bounded by the client timeout and
// decodes a 200 answer into out.
func (c *client) do(ctx context.Context, method, path string, body, out any) (err error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	tok, err := c.token(ctx)
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}
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
	tok.SetAuthHeader(req)
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
	return decodeAnswer(resp.StatusCode, raw, out)
}

func decodeAnswer(status int, raw []byte, out any) error {
	if status != http.StatusOK {
		var e struct {
			Error struct {
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if jerr := json.Unmarshal(raw, &e); jerr != nil {
			return &apiError{status: status, message: "undecodable error body"}
		}
		return &apiError{status: status, code: e.Error.Status, message: e.Error.Message}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode answer: %w", err)
	}
	return nil
}

// normalize checks cfg before any I/O and fills defaults.
func normalize(cfg Config) (Config, *url.URL, error) {
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	base, err := parseEndpoint(cfg.Endpoint)
	if err != nil {
		return cfg, nil, err
	}
	if !validKeyVersion(cfg.Key) {
		return cfg, nil, fmt.Errorf("%w: key %q: want projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<k>/cryptoKeyVersions/<n>", ErrInvalidConfig, cfg.Key)
	}
	if cfg.Timeout < 0 {
		return cfg, nil, fmt.Errorf("%w: timeout %v", ErrInvalidConfig, cfg.Timeout)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	return cfg, base, nil
}

// parseEndpoint accepts http(s)://host[:port][/path].
func parseEndpoint(endpoint string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: endpoint: %w", ErrInvalidConfig, err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%w: endpoint %q: want http(s)://host[:port][/path]", ErrInvalidConfig, endpoint)
	}
	return u, nil
}

// keyVersionPath is the collection names of a key version name, in order.
var keyVersionPath = [...]string{"projects", "locations", "keyRings", "cryptoKeys", "cryptoKeyVersions"}

// validKeyVersion checks the five name/ID pairs; IDs start with a letter or
// digit, so no "." or ".." segment can escape /v1/.
func validKeyVersion(key string) bool {
	parts := strings.Split(key, "/")
	if len(parts) != 2*len(keyVersionPath) {
		return false
	}
	for i, collection := range keyVersionPath {
		if parts[2*i] != collection || !validID(parts[2*i+1], i == 0) {
			return false
		}
	}
	return isDigits(parts[len(parts)-1])
}

// validID accepts [A-Za-z0-9][A-Za-z0-9_-]*, plus "." and ":" in project
// IDs (domain-scoped projects).
func validID(id string, project bool) bool {
	if id == "" || !isAlnum(id[0]) {
		return false
	}
	for i := range len(id) {
		c := id[i]
		if !isAlnum(c) && c != '_' && c != '-' && (!project || c != '.' && c != ':') {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

func isAlnum(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}
