// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pgx_test

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/golusoris/golusoris/core/tlsx/tlsxtest"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
)

func writeSecret(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConnString_KeepsDSNWithoutOptions(t *testing.T) {
	t.Parallel()
	for _, dsn := range []string{
		"postgres://app:pw@db.example:5432/app?sslmode=require",
		"host=db.example user=app sslmode=require",
	} {
		got, err := dbpgx.Options{}.ConnString(t.Context(), dsn)
		if err != nil || got != dsn {
			t.Fatalf("ConnString(%q) = %q, %v; want the DSN unchanged", dsn, got, err)
		}
	}
}

func TestConnString_OverridesDSN(t *testing.T) {
	t.Parallel()
	const password = `p@ss:w/rd?&= %+`
	opts := dbpgx.Options{
		PasswordFile: writeSecret(t, t.TempDir(), "password", password+"\n"),
		SSL:          dbpgx.SSLOptions{Mode: "disable"},
	}
	for name, dsn := range map[string]string{
		"url":     "postgres://app:old@db.example:5432/app?sslmode=require&application_name=svc",
		"keyword": "host=db.example user=app password=old sslmode=require application_name=svc",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := opts.ConnString(t.Context(), dsn)
			if err != nil {
				t.Fatalf("ConnString: %v", err)
			}
			cfg, err := pgconn.ParseConfig(got)
			if err != nil {
				t.Fatalf("ParseConfig(%q): %v", got, err)
			}
			if cfg.TLSConfig != nil || len(cfg.Fallbacks) != 0 {
				t.Fatal("db.ssl.mode=disable did not override sslmode=require in the DSN")
			}
			if cfg.Password != password {
				t.Fatalf("password = %q, want the file's", cfg.Password)
			}
			if cfg.User != "app" || cfg.RuntimeParams["application_name"] != "svc" {
				t.Fatalf("user %q or application_name %q lost", cfg.User, cfg.RuntimeParams["application_name"])
			}
		})
	}
	got, err := opts.ConnString(t.Context(), "postgres://app:old@db.example/app?sslmode=require")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if modes := u.Query()["sslmode"]; len(modes) != 1 || modes[0] != "disable" {
		t.Fatalf("sslmode values = %v, want exactly [disable]", modes)
	}
}

func TestConnString_AddsAbsentParameters(t *testing.T) {
	t.Parallel()
	// A directory with a space: the query must keep it readable for pgconn.
	dir := filepath.Join(t.TempDir(), "ca dir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(dir, "ca.crt")
	tlsxtest.NewCA(t).WriteCA(t, caPath)
	opts := dbpgx.Options{SSL: dbpgx.SSLOptions{Mode: "verify-full", RootCert: caPath}}

	got, err := opts.ConnString(t.Context(), "postgres://app@db.example:5432/app")
	if err != nil {
		t.Fatalf("ConnString: %v", err)
	}
	cfg, err := pgconn.ParseConfig(got)
	if err != nil {
		t.Fatalf("ParseConfig(%q): %v", got, err)
	}
	if cfg.TLSConfig == nil || cfg.TLSConfig.ServerName != "db.example" || cfg.TLSConfig.RootCAs == nil {
		t.Fatalf("verify-full with the CA file not applied: %+v", cfg.TLSConfig)
	}
	// Checked on the URL: pgconn fills an absent password from PGPASSWORD, which hosted Windows runners set.
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if _, set := u.User.Password(); set {
		t.Fatalf("ConnString added a password without a password file: %q", got)
	}
}

func TestConnString_PasswordFileErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, path := range map[string]string{
		"missing": filepath.Join(dir, "missing"),
		"empty":   writeSecret(t, dir, "empty", "\n"),
	} {
		if _, err := (dbpgx.Options{PasswordFile: path}).ConnString(t.Context(), "postgres://app@db.example/app"); err == nil {
			t.Fatalf("%s password file accepted", name)
		}
	}
}
