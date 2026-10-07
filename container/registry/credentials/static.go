// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package credentials

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxSecretBytes caps one secret file; registry secrets are tokens, not blobs.
const maxSecretBytes = 64 << 10

// ErrInvalidStatic reports an unusable static credential entry.
var ErrInvalidStatic = errors.New("credentials: invalid static entry")

// StaticOptions binds one registry host to secrets mounted as files (a
// Kubernetes Secret volume, a systemd credential). Files are re-read on every
// lookup so rotated secrets apply without a restart.
type StaticOptions struct {
	// Host is the registry host ("ghcr.io", "harbor.example.com:8443").
	Host string `koanf:"host"`
	// Username is a literal, non-secret user name (robot account, "AWS",
	// "oauth2accesstoken"). Mutually exclusive with UsernameFile.
	Username string `koanf:"username"`
	// UsernameFile holds the user name.
	UsernameFile string `koanf:"username_file"`
	// PasswordFile holds the password or personal access token.
	PasswordFile string `koanf:"password_file"`
	// IdentityTokenFile holds an identity (refresh) token instead of a
	// username/password pair.
	IdentityTokenFile string `koanf:"identity_token_file"`
}

func (o StaticOptions) validate() error {
	if o.Host == "" {
		return fmt.Errorf("%w: empty host", ErrInvalidStatic)
	}
	if o.Username != "" && o.UsernameFile != "" {
		return fmt.Errorf("%w: %s: username and username_file both set", ErrInvalidStatic, o.Host)
	}
	basic := o.PasswordFile != ""
	token := o.IdentityTokenFile != ""
	if basic == token {
		return fmt.Errorf("%w: %s: set exactly one of password_file or identity_token_file", ErrInvalidStatic, o.Host)
	}
	if basic && o.Username == "" && o.UsernameFile == "" {
		return fmt.Errorf("%w: %s: password_file needs username or username_file", ErrInvalidStatic, o.Host)
	}
	return nil
}

// StaticFiles serves credentials from secret files, keyed by host.
type StaticFiles struct {
	entries map[string]StaticOptions
}

// NewStaticFiles validates entries; duplicate hosts are rejected.
func NewStaticFiles(entries []StaticOptions) (*StaticFiles, error) {
	byHost := make(map[string]StaticOptions, len(entries))
	for _, e := range entries {
		if err := e.validate(); err != nil {
			return nil, err
		}
		if _, dup := byHost[e.Host]; dup {
			return nil, fmt.Errorf("%w: duplicate host %q", ErrInvalidStatic, e.Host)
		}
		byHost[e.Host] = e
	}
	return &StaticFiles{entries: byHost}, nil
}

// Credential implements [Provider].
func (s *StaticFiles) Credential(_ context.Context, host string) (Credential, error) {
	e, ok := s.entries[host]
	if !ok {
		return Credential{}, ErrNoCredential
	}
	if e.IdentityTokenFile != "" {
		tok, err := readSecret(e.IdentityTokenFile)
		if err != nil {
			return Credential{}, err
		}
		return Credential{RefreshToken: tok}, nil
	}
	user := e.Username
	if e.UsernameFile != "" {
		u, err := readSecret(e.UsernameFile)
		if err != nil {
			return Credential{}, err
		}
		user = u
	}
	pass, err := readSecret(e.PasswordFile)
	if err != nil {
		return Credential{}, err
	}
	return Credential{Username: user, Password: pass}, nil
}

// readSecret reads one bounded secret file and trims the trailing newline
// that secret mounts and editors usually add.
func readSecret(path string) (_ string, err error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("credentials: open secret: %w", err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	b, err := io.ReadAll(io.LimitReader(f, maxSecretBytes+1))
	if err != nil {
		return "", fmt.Errorf("credentials: read secret %s: %w", path, err)
	}
	if len(b) > maxSecretBytes {
		return "", fmt.Errorf("%w: secret %s exceeds %d bytes", ErrInvalidStatic, path, maxSecretBytes)
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("%w: secret %s is empty", ErrInvalidStatic, path)
	}
	return v, nil
}
