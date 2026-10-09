// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultTokenPath is where cosign's filesystem provider reads an OIDC
	// token. Mount a projected service-account token there whose audience is
	// "sigstore", the audience Fulcio accepts.
	DefaultTokenPath = "/var/run/sigstore/cosign/oidc-token" // #nosec G101 -- a file path, not a credential.
	// DefaultAudience is the token audience Fulcio's public instance accepts.
	DefaultAudience = "sigstore"
	// DefaultTokenTimeout bounds one GitHub Actions token request when
	// GitHubToken.Timeout is zero.
	DefaultTokenTimeout = 30 * time.Second

	// GitHub Actions sets both for a job with `permissions: id-token: write`.
	envGitHubRequestURL   = "ACTIONS_ID_TOKEN_REQUEST_URL"
	envGitHubRequestToken = "ACTIONS_ID_TOKEN_REQUEST_TOKEN" // #nosec G101 -- environment variable name, not a credential.

	// maxTokenBytes caps a token file or response; OIDC JWTs are a few KiB.
	maxTokenBytes = 64 << 10
)

// ErrNoToken reports an ID token source with nothing to offer: unset GitHub
// Actions variables, or an empty token.
var ErrNoToken = errors.New("sign: no ID token")

// FileToken reads an OIDC identity token from a file on every call, so a
// projected Kubernetes service-account token is picked up after the kubelet
// rotates it. Use its Token method as [Signer].IDToken.
type FileToken struct {
	// Path of the token file. Empty uses [DefaultTokenPath].
	Path string `koanf:"path"`
}

// Token returns the trimmed file content. The token is never logged or put
// into an error.
func (f FileToken) Token(ctx context.Context) (_ string, err error) {
	if err = ctx.Err(); err != nil {
		return "", fmt.Errorf("sign: token file: %w", err)
	}
	path := f.Path
	if path == "" {
		path = DefaultTokenPath
	}
	fh, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("sign: token file: %w", err)
	}
	defer func() { err = errors.Join(err, fh.Close()) }()
	raw, err := io.ReadAll(io.LimitReader(fh, maxTokenBytes+1))
	if err != nil {
		return "", fmt.Errorf("sign: read token file %s: %w", path, err)
	}
	return checkToken(raw, "token file "+path)
}

// GitHubToken requests an OIDC identity token from the GitHub Actions
// runtime, the token `cosign sign` uses in a workflow. The job needs
// `permissions: id-token: write`, which sets ACTIONS_ID_TOKEN_REQUEST_URL and
// ACTIONS_ID_TOKEN_REQUEST_TOKEN; both are read on every call. Use its Token
// method as [Signer].IDToken. Requests are not retried.
type GitHubToken struct {
	// Audience of the requested token. Empty uses [DefaultAudience].
	Audience string `koanf:"audience"`
	// Timeout bounds the request. Zero uses [DefaultTokenTimeout].
	Timeout time.Duration `koanf:"timeout"`
	// Transport carries the request. Nil uses a private clone of
	// [http.DefaultTransport].
	Transport http.RoundTripper `koanf:"-"`
}

// Token requests a token for g.Audience. Neither the request bearer nor the
// returned token is logged or put into an error.
func (g GitHubToken) Token(ctx context.Context) (string, error) {
	reqURL, bearer := os.Getenv(envGitHubRequestURL), os.Getenv(envGitHubRequestToken)
	if reqURL == "" || bearer == "" {
		return "", fmt.Errorf("%w: %s or %s unset; the job needs permissions id-token: write", ErrNoToken, envGitHubRequestURL, envGitHubRequestToken)
	}
	u, err := githubTokenURL(reqURL, g.Audience)
	if err != nil {
		return "", err
	}
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = DefaultTokenTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	transport, release := ownTransport(g.Transport)
	defer release()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody) // #nosec G704 -- URL is the GitHub Actions runtime endpoint from the job environment.
	if err != nil {
		return "", fmt.Errorf("sign: github token request: %w", err)
	}
	req.Header.Set("Authorization", "bearer "+bearer)
	req.Header.Set("Accept", "application/json")
	return readGitHubToken(&http.Client{Transport: transport, Timeout: timeout}, req)
}

// githubTokenURL adds the audience to the runtime's request URL, which
// already carries an api-version query.
func githubTokenURL(raw, audience string) (string, error) {
	if err := checkURL(raw); err != nil {
		return "", fmt.Errorf("%s: %w", envGitHubRequestURL, err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", ErrInvalidOptions, envGitHubRequestURL, err)
	}
	if audience == "" {
		audience = DefaultAudience
	}
	q := u.Query()
	q.Set("audience", audience)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func readGitHubToken(c *http.Client, req *http.Request) (_ string, err error) {
	resp, err := c.Do(req) // #nosec G704 -- URL is the GitHub Actions runtime endpoint from the job environment.
	if err != nil {
		return "", fmt.Errorf("sign: github token: %w", err)
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sign: github token: status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenBytes+1))
	if err != nil {
		return "", fmt.Errorf("sign: github token: read response: %w", err)
	}
	if len(raw) > maxTokenBytes {
		return "", fmt.Errorf("%w: github token response > %d bytes", ErrInvalidOptions, maxTokenBytes)
	}
	var body struct {
		Value string `json:"value"`
	}
	if err = json.Unmarshal(raw, &body); err != nil {
		return "", errors.New("sign: github token: response is not JSON")
	}
	return checkToken([]byte(body.Value), "github token")
}

// checkToken trims raw and refuses an empty or oversized token.
func checkToken(raw []byte, what string) (string, error) {
	if len(raw) > maxTokenBytes {
		return "", fmt.Errorf("%w: %s > %d bytes", ErrInvalidOptions, what, maxTokenBytes)
	}
	tok := strings.TrimSpace(string(raw))
	if tok == "" {
		return "", fmt.Errorf("%w: %s is empty", ErrNoToken, what)
	}
	return tok, nil
}
