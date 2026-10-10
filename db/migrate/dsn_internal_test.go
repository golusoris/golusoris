// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package migrate

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gomigrate "github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/golusoris/golusoris/core/tlsx/tlsxtest"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
)

// driverConfig parses migratorURL's result the way golang-migrate's pgx/v5
// driver does: scheme back to postgres, x- parameters filtered, query re-encoded.
func driverConfig(t *testing.T, migrateURL string) *pgconn.Config {
	t.Helper()
	u, err := url.Parse(migrateURL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "pgx5" {
		t.Fatalf("scheme = %q, want pgx5", u.Scheme)
	}
	u.Scheme = "postgres"
	cfg, err := pgconn.ParseConfig(gomigrate.FilterCustomQuery(u).String())
	if err != nil {
		t.Fatalf("driver ParseConfig: %v", err)
	}
	return cfg
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestMigratorURL_AppliesPoolSSLAndPasswordFile pins #771: the migrator's
// connection honours db.ssl.* and db.password_file like the pool.
func TestMigratorURL_AppliesPoolSSLAndPasswordFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	tlsxtest.NewCA(t).WriteCA(t, caPath)
	opts := dbpgx.Options{
		PasswordFile: writeFile(t, filepath.Join(dir, "password"), "from-file\n"),
		SSL:          dbpgx.SSLOptions{Mode: "verify-full", RootCert: caPath},
	}
	for name, dsn := range map[string]string{
		"absent":   "postgres://app@db.example:5432/app?x-migrations-table=schema_migrations",
		"override": "postgresql://app:old@db.example:5432/app?sslmode=disable&sslrootcert=/nonexistent",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := migratorURL(dsn, opts)
			if err != nil {
				t.Fatalf("migratorURL: %v", err)
			}
			cfg := driverConfig(t, got)
			if cfg.TLSConfig == nil || cfg.TLSConfig.ServerName != "db.example" || cfg.TLSConfig.RootCAs == nil {
				t.Fatalf("db.ssl.mode=verify-full with db.ssl.rootcert not applied: %+v", cfg.TLSConfig)
			}
			if cfg.Password != "from-file" {
				t.Fatalf("password = %q, want the password file's", cfg.Password)
			}
		})
	}
}

func TestMigratorURL_KeepsDSNWithoutOptions(t *testing.T) {
	t.Parallel()
	const dsn = "postgres://app:pw@db.example:5432/app?sslmode=disable&x-migrations-table=m"
	got, err := migratorURL(dsn, dbpgx.Options{})
	if err != nil {
		t.Fatalf("migratorURL: %v", err)
	}
	if want := "pgx5" + strings.TrimPrefix(dsn, "postgres"); got != want {
		t.Fatalf("migratorURL = %q, want %q", got, want)
	}
	if cfg := driverConfig(t, got); cfg.TLSConfig != nil || cfg.Password != "pw" {
		t.Fatalf("DSN settings changed: tls=%v password=%q", cfg.TLSConfig != nil, cfg.Password)
	}
}

func TestMigratorURL_Refusals(t *testing.T) {
	t.Parallel()
	if _, err := migratorURL("host=db.example user=app", dbpgx.Options{}); err == nil ||
		!strings.Contains(err.Error(), "keyword/value form is not supported") {
		t.Fatalf("keyword/value DSN error = %v", err)
	}
	spaced := dbpgx.Options{SSL: dbpgx.SSLOptions{Mode: "verify-full", RootCert: "/etc/ca dir/ca.crt"}}
	if _, err := migratorURL("postgres://app@db.example/app", spaced); err == nil ||
		!strings.Contains(err.Error(), "contains a space") {
		t.Fatalf("spaced db.ssl.rootcert error = %v", err)
	}
	missing := dbpgx.Options{PasswordFile: filepath.Join(t.TempDir(), "missing")}
	if _, err := migratorURL("postgres://app@db.example/app", missing); err == nil ||
		!strings.Contains(err.Error(), "db/migrate: apply db.ssl and db.password_file") {
		t.Fatalf("missing password file error = %v", err)
	}
}
