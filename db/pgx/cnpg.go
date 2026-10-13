// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pgx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/secrets"
)

// SSLOptions points at TLS material mounted from secrets (CloudNativePG
// client certificates, cluster CA). Set values override the same libpq
// parameters in the DSN; files are re-read for every new connection so a
// rotated certificate applies without a restart.
type SSLOptions struct {
	// Mode is libpq sslmode (disable, allow, prefer, require, verify-ca, verify-full).
	Mode string `koanf:"mode"`
	// RootCert is the CA bundle file (libpq sslrootcert).
	RootCert string `koanf:"rootcert"`
	// Cert is the client certificate file (libpq sslcert); requires Key.
	Cert string `koanf:"cert"`
	// Key is the client private key file (libpq sslkey); requires Cert.
	Key string `koanf:"key"`
}

func (s SSLOptions) hasFiles() bool {
	return s.RootCert != "" || s.Cert != "" || s.Key != ""
}

func (s SSLOptions) params() [][2]string {
	all := [][2]string{{"sslmode", s.Mode}, {"sslrootcert", s.RootCert}, {"sslcert", s.Cert}, {"sslkey", s.Key}}
	set := make([][2]string, 0, len(all))
	for _, kv := range all {
		if kv[1] != "" {
			set = append(set, kv)
		}
	}
	return set
}

// ReadPool is the pool for read-only queries, a distinct type so fx can
// provide it next to the primary [*pgxpool.Pool]. With db.read_dsn set it
// is its own pool whose sessions default to read-only transactions
// (CloudNativePG "-ro" service); unset, it is the primary pool.
type ReadPool struct {
	*pgxpool.Pool
}

// NewReadPool connects the read pool described by opts.ReadDSN, sharing
// every other option with the primary pool. Callers must Close it.
func NewReadPool(ctx context.Context, opts Options, logger *slog.Logger, clk clock.Clock) (*ReadPool, error) {
	if opts.ReadDSN == "" {
		return nil, errors.New("db/pgx: read DSN is required")
	}
	readOpts := opts
	readOpts.DSN = opts.ReadDSN
	pool, err := newPool(ctx, readOpts, logger, clk, true)
	if err != nil {
		return nil, err
	}
	return &ReadPool{Pool: pool}, nil
}

func provideReadPool(lc fx.Lifecycle, opts Options, primary *pgxpool.Pool, logger *slog.Logger, clk clock.Clock) (*ReadPool, error) {
	if opts.ReadDSN == "" {
		return &ReadPool{Pool: primary}, nil
	}
	// Not the fx start ctx: the pool outlives it; the budget covers every attempt.
	startCtx, cancel := context.WithTimeout(context.Background(), opts.connectBudget())
	defer cancel()
	read, err := NewReadPool(startCtx, opts, logger, clk)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.StopHook(read.Close))
	return read, nil
}

// withConnParams appends libpq parameters to a URL or keyword/value DSN;
// later occurrences win in pgconn, so set values override the DSN.
func withConnParams(dsn string, params [][2]string) string {
	if len(params) == 0 {
		return dsn
	}
	var b strings.Builder
	b.WriteString(dsn)
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		for _, kv := range params {
			b.WriteString(sep + uriEscape(kv[0]) + "=" + uriEscape(kv[1]))
			sep = "&"
		}
		return b.String()
	}
	quoter := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	for _, kv := range params {
		b.WriteString(" " + kv[0] + "='" + quoter.Replace(kv[1]) + "'")
	}
	return b.String()
}

// uriEscape percent-encodes for pgconn's URI parser, which decodes %XX
// only: a QueryEscape "+" would arrive as a literal plus.
func uriEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// ConnString returns dsn with db.ssl.* and db.password_file applied, for a
// connection opened outside the pool, such as the migrator's. Set SSL options
// replace the same DSN parameters and the file's password replaces the DSN's,
// as in the pool. The files are read once here: such a connection is not
// re-keyed when they rotate.
func (o Options) ConnString(ctx context.Context, dsn string) (string, error) {
	params := o.SSL.params()
	if len(params) == 0 && o.PasswordFile == "" {
		return dsn, nil
	}
	password := ""
	if o.PasswordFile != "" {
		var err error
		if password, err = readPasswordFile(ctx, o.PasswordFile); err != nil {
			return "", err
		}
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" || u.Host == "" {
		if password != "" {
			params = append(params, [2]string{"password", password})
		}
		return withConnParams(dsn, params), nil
	}
	return urlConnString(u, params, password), nil
}

// urlConnString replaces the parameters instead of appending them: URL
// consumers that re-encode the query keep every duplicate, and not all of them
// let the last one win.
func urlConnString(u *url.URL, params [][2]string, password string) string {
	query := u.Query()
	for _, kv := range params {
		query.Set(kv[0], kv[1])
	}
	u.RawQuery = encodeURIQuery(query)
	if password != "" {
		u.User = url.UserPassword(u.User.Username(), password)
	}
	return u.String()
}

// encodeURIQuery encodes query in key order with uriEscape.
func encodeURIQuery(query url.Values) string {
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		for _, value := range query[key] {
			pairs = append(pairs, uriEscape(key)+"="+uriEscape(value))
		}
	}
	return strings.Join(pairs, "&")
}

// readPasswordFile reads a mounted secret through [secrets.File], which
// bounds the size, refuses non-regular files, and trims the trailing newline
// that `kubectl create secret --from-file` and editors leave behind.
func readPasswordFile(ctx context.Context, path string) (string, error) {
	password, err := secrets.File(filepath.Dir(path)).Get(ctx, filepath.Base(path))
	if err != nil {
		return "", fmt.Errorf("db/pgx: password file: %w", err)
	}
	if password == "" {
		return "", errors.New("db/pgx: password file is empty")
	}
	return password, nil
}

// secretRefresher re-reads the password file and TLS files before each new
// connection, so rotated CloudNativePG secrets apply to new connections.
type secretRefresher struct {
	passwordFile string
	tlsDSN       string
}

func (s secretRefresher) beforeConnect(ctx context.Context, cc *pgx.ConnConfig) error {
	if s.passwordFile != "" {
		password, err := readPasswordFile(ctx, s.passwordFile)
		if err != nil {
			return err
		}
		cc.Password = password
	}
	if s.tlsDSN == "" {
		return nil
	}
	fresh, err := pgconn.ParseConfig(s.tlsDSN)
	if err != nil {
		return fmt.Errorf("db/pgx: reload TLS files: %w", err)
	}
	// Replace fallbacks too: a stale "prefer" fallback could keep old material.
	cc.TLSConfig = fresh.TLSConfig
	cc.Fallbacks = fresh.Fallbacks
	return nil
}

// applySecrets wires PasswordFile and SSL file options into cfg. dsn is the
// DSN with SSL parameters already appended.
func applySecrets(ctx context.Context, cfg *pgxpool.Config, dsn string, opts Options) error {
	refresher := secretRefresher{passwordFile: opts.PasswordFile}
	if opts.SSL.hasFiles() {
		refresher.tlsDSN = dsn
	}
	if refresher.passwordFile == "" && refresher.tlsDSN == "" {
		return nil
	}
	if refresher.passwordFile != "" {
		password, err := readPasswordFile(ctx, refresher.passwordFile)
		if err != nil {
			return err
		}
		cfg.ConnConfig.Password = password
	}
	cfg.BeforeConnect = refresher.beforeConnect
	return nil
}
