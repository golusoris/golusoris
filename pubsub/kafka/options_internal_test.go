// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package kafka

import (
	"bytes"
	"crypto/tls"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"

	"github.com/golusoris/golusoris/core/tlsx"
	"github.com/golusoris/golusoris/core/tlsx/tlsxtest"
)

// writeTLSFiles writes a throwaway CA plus a client certificate and key.
func writeTLSFiles(t *testing.T) tlsx.Files {
	t.Helper()
	ca := tlsxtest.NewCA(t)
	return ca.WriteFiles(t, t.TempDir(), ca.Client(t, "client"))
}

// built constructs an unconnected kgo client so tests can read option values.
func built(t *testing.T, cfg Config) *kgo.Client {
	t.Helper()
	cfg.Brokers = []string{"127.0.0.1:1"}
	opts, err := clientOptions(cfg, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	kc, err := kgo.NewClient(opts...)
	require.NoError(t, err)
	t.Cleanup(kc.Close)
	return kc
}

func mechanismName(t *testing.T, kc *kgo.Client) string {
	t.Helper()
	mechanisms, ok := kc.OptValue(kgo.SASL).([]sasl.Mechanism)
	if !ok || len(mechanisms) == 0 {
		return ""
	}
	require.Len(t, mechanisms, 1)
	return mechanisms[0].Name()
}

func TestClientOptionsTLS(t *testing.T) {
	t.Parallel()
	paths := writeTLSFiles(t)

	require.Nil(t, built(t, Config{}).OptValue(kgo.DialTLSConfig))

	systemRoots, ok := built(t, Config{TLS: true}).OptValue(kgo.DialTLSConfig).(*tls.Config)
	require.True(t, ok, "tls: true enables TLS")
	require.Nil(t, systemRoots.RootCAs)
	require.Equal(t, uint16(tls.VersionTLS12), systemRoots.MinVersion)

	privateCA, ok := built(t, Config{CA: paths.CA}).OptValue(kgo.DialTLSConfig).(*tls.Config)
	require.True(t, ok, "ca implies TLS")
	require.NotNil(t, privateCA.RootCAs)
	require.Equal(t, uint16(tls.VersionTLS12), privateCA.MinVersion)

	_, err := clientOptions(Config{CA: filepath.Join(t.TempDir(), "missing.pem")}, slog.New(slog.DiscardHandler))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = clientOptions(Config{CA: paths.Key}, slog.New(slog.DiscardHandler))
	require.ErrorIs(t, err, tlsx.ErrEmptyCA)
}

func TestClientOptionsSASLMechanisms(t *testing.T) {
	t.Parallel()
	require.Empty(t, mechanismName(t, built(t, Config{})))
	for mechanism, want := range map[string]string{
		"PLAIN":         SASLPlain,
		"plain":         SASLPlain,
		"SCRAM-SHA-256": SASLScramSHA256,
		"scram-sha-512": SASLScramSHA512,
	} {
		kc := built(t, Config{TLS: true, SASL: SASLConfig{Mechanism: mechanism, User: "u", Password: "p"}})
		require.Equal(t, want, mechanismName(t, kc), mechanism)
	}
}

func TestClientOptionsSASLPasswordFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "password")
	require.NoError(t, os.WriteFile(path, []byte("s3cret\r\n"), 0o600))
	password, err := saslPassword(SASLConfig{User: "u", PasswordFile: path})
	require.NoError(t, err)
	require.Equal(t, "s3cret", password, "trailing line breaks are trimmed")

	kc := built(t, Config{CA: writeTLSFiles(t).CA, SASL: SASLConfig{
		Mechanism: SASLScramSHA512, User: "u", PasswordFile: path,
	}})
	require.Equal(t, SASLScramSHA512, mechanismName(t, kc))
}

func TestClientOptionsSASLRejects(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	blank := filepath.Join(dir, "blank")
	require.NoError(t, os.WriteFile(blank, []byte("\n"), 0o600))
	tests := map[string]struct {
		cfg  SASLConfig
		want error
	}{
		"unknown mechanism":       {SASLConfig{Mechanism: "GSSAPI", User: "u", Password: "p"}, ErrUnsupportedSASLMechanism},
		"missing user":            {SASLConfig{Mechanism: SASLPlain, Password: "p"}, ErrSASLCredentials},
		"missing password":        {SASLConfig{Mechanism: SASLPlain, User: "u"}, ErrSASLCredentials},
		"password and file":       {SASLConfig{Mechanism: SASLPlain, User: "u", Password: "p", PasswordFile: blank}, ErrSASLCredentials},
		"blank password file":     {SASLConfig{Mechanism: SASLPlain, User: "u", PasswordFile: blank}, ErrSASLCredentials},
		"missing password file":   {SASLConfig{Mechanism: SASLPlain, User: "u", PasswordFile: filepath.Join(dir, "nope")}, os.ErrNotExist},
		"user without mechanism":  {SASLConfig{User: "u"}, ErrSASLCredentials},
		"password without method": {SASLConfig{Password: "p"}, ErrSASLCredentials},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := clientOptions(Config{SASL: tt.cfg}, slog.New(slog.DiscardHandler))
			require.ErrorIs(t, err, tt.want)
		})
	}
}

func TestClientOptionsWarnsPlainWithoutTLS(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	_, err := clientOptions(Config{SASL: SASLConfig{Mechanism: "plain", User: "u", Password: "p"}}, logger)
	require.NoError(t, err)
	require.Contains(t, logs.String(), "clear text")

	logs.Reset()
	_, err = clientOptions(Config{TLS: true, SASL: SASLConfig{Mechanism: SASLPlain, User: "u", Password: "p"}}, logger)
	require.NoError(t, err)
	require.Empty(t, logs.String())
}
