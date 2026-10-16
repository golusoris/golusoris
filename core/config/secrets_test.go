// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/core/config"
)

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// kubernetesSecretDir mirrors a secret volume: real files under a dated
// dir, ..data pointing at it, and per-key symlinks through ..data.
func kubernetesSecretDir(t *testing.T, values map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for key, value := range values {
		writeFile(t, filepath.Join(dir, "..2026_10_07_00_00_00.1", key), value)
		if err := os.Symlink(filepath.Join("..data", key), filepath.Join(dir, key)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("..2026_10_07_00_00_00.1", filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSecretDirKubernetesLayout(t *testing.T) {
	t.Parallel()
	dir := kubernetesSecretDir(t, map[string]string{"db.dsn": "postgres://app@db/app\n", "db.password_file": "x"})
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".hidden"), "x")
	c, err := config.New(config.Options{EnvPrefix: "CFGTEST_", SecretDirs: []string{dir, filepath.Join(dir, "missing")}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.String("db.dsn"); got != "postgres://app@db/app" {
		t.Fatalf("db.dsn = %q", got)
	}
	if got := c.String("db.password_file"); got != "x" {
		t.Fatalf("underscore key from file name = %q", got)
	}
	for key := range c.All() {
		if strings.HasPrefix(key, ".") || strings.HasPrefix(key, "nested") {
			t.Fatalf("plumbing entry %q leaked into config", key)
		}
	}
}

func TestSecretDirSizeBoundary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "token"), "12345678")
	c, err := config.New(config.Options{EnvPrefix: "CFGTEST_", SecretDirs: []string{dir}, MaxSecretBytes: 8})
	if err != nil || c.String("token") != "12345678" {
		t.Fatalf("secret at limit: %v", err)
	}
	writeFile(t, filepath.Join(dir, "token"), "123456789")
	_, err = config.New(config.Options{EnvPrefix: "CFGTEST_", SecretDirs: []string{dir}, MaxSecretBytes: 8})
	if !errors.Is(err, config.ErrSecretTooLarge) {
		t.Fatalf("secret over limit: error = %v, want ErrSecretTooLarge", err)
	}
}

func TestSecretDirRejectsUnsafeEntries(t *testing.T) {
	t.Parallel()
	outside := writeFile(t, filepath.Join(t.TempDir(), "host-secret"), "leak")
	escaping := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(escaping, "db.dsn")); err != nil {
		t.Fatal(err)
	}
	badKey := t.TempDir()
	writeFile(t, filepath.Join(badKey, "db..dsn"), "x")
	for name, dir := range map[string]string{"symlink escaping the dir": escaping, "empty key segment": badKey} {
		if _, err := config.New(config.Options{EnvPrefix: "CFGTEST_", SecretDirs: []string{dir}}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := config.New(config.Options{MaxSecretBytes: -1}); err == nil {
		t.Error("negative MaxSecretBytes accepted")
	}
}

// TestPrecedence pins files < secret dirs < env < *_FILE.
func TestPrecedence(t *testing.T) {
	cfgFile := writeFile(t, filepath.Join(t.TempDir(), "config.yaml"), "a: file\nb: file\nc: file\nd: file\n")
	secretDir := t.TempDir()
	for _, key := range []string{"b", "c", "d"} {
		writeFile(t, filepath.Join(secretDir, key), "secret")
	}
	t.Setenv("CFGPREC_C", "env")
	t.Setenv("CFGPREC_D_FILE", writeFile(t, filepath.Join(t.TempDir(), "d"), "file-env\n"))

	c, err := config.New(config.Options{
		EnvPrefix: "CFGPREC_", Files: []string{cfgFile}, SecretDirs: []string{secretDir}, FileEnvSuffix: "_FILE",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := map[string]string{"a": "file", "b": "secret", "c": "env", "d": "file-env"}
	for key, value := range want {
		if got := c.String(key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if c.Exists("d.file") {
		t.Error("*_FILE variable also loaded as a plain key")
	}
}

func TestFileEnvConflictFails(t *testing.T) {
	t.Setenv("CFGCONF_DB_DSN", "plain")
	t.Setenv("CFGCONF_DB_DSN_FILE", writeFile(t, filepath.Join(t.TempDir(), "dsn"), "from-file"))
	_, err := config.New(config.Options{EnvPrefix: "CFGCONF_", FileEnvSuffix: "_FILE"})
	if err == nil || !strings.Contains(err.Error(), "both CFGCONF_DB_DSN and CFGCONF_DB_DSN_FILE") {
		t.Fatalf("error = %v, want conflict", err)
	}
}

func TestFileEnvCompoundKeyStaysPlain(t *testing.T) {
	t.Setenv("CFGCOMP_TLS_CERT_FILE", "/etc/tls/tls.crt")
	c, err := config.New(config.Options{
		EnvPrefix: "CFGCOMP_", FileEnvSuffix: "_FILE", CompoundKeys: []string{"tls.cert_file"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.String("tls.cert_file"); got != "/etc/tls/tls.crt" {
		t.Fatalf("tls.cert_file = %q, want the path verbatim", got)
	}
}

func TestFileEnvDisabledByDefault(t *testing.T) {
	t.Setenv("CFGOFF_DB_DSN_FILE", "/run/secrets/dsn")
	c, err := config.New(config.Options{EnvPrefix: "CFGOFF_"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.String("db.dsn.file"); got != "/run/secrets/dsn" {
		t.Fatalf("default mapping changed: db.dsn.file = %q", got)
	}
}

func TestFileEnvFailures(t *testing.T) {
	t.Setenv("CFGFAIL_TOKEN_FILE", filepath.Join(t.TempDir(), "missing"))
	if _, err := config.New(config.Options{EnvPrefix: "CFGFAIL_", FileEnvSuffix: "_FILE"}); err == nil {
		t.Error("missing *_FILE target accepted")
	}
	t.Setenv("CFGFAIL_TOKEN_FILE", t.TempDir())
	if _, err := config.New(config.Options{EnvPrefix: "CFGFAIL_", FileEnvSuffix: "_FILE"}); err == nil {
		t.Error("directory *_FILE target accepted")
	}
	if _, err := config.New(config.Options{FileEnvSuffix: "_FILE"}); err == nil {
		t.Error("FileEnvSuffix without EnvPrefix accepted")
	}
}

func TestFileEnvTargetsCompoundKey(t *testing.T) {
	t.Setenv("CFGTGT_DB_READ_DSN_FILE", writeFile(t, filepath.Join(t.TempDir(), "ro"), "postgres://ro\n"))
	c, err := config.New(config.Options{
		EnvPrefix: "CFGTGT_", FileEnvSuffix: "_FILE", CompoundKeys: []string{"db.read_dsn"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.String("db.read_dsn"); got != "postgres://ro" {
		t.Fatalf("db.read_dsn = %q", got)
	}
	if c.Exists("db.read.dsn.file") {
		t.Fatal("*_FILE variable also loaded as a plain key")
	}
}
