// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var errPingRefused = errors.New("ping refused")

// pingFailDriver connects and then refuses the ping, the one step a real
// database cannot be made to fail on demand.
type pingFailDriver struct{}

func (pingFailDriver) Open(string) (driver.Conn, error) { return pingFailConn{}, nil }

type pingFailConn struct{}

func (pingFailConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (pingFailConn) Close() error                        { return nil }
func (pingFailConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (pingFailConn) Ping(context.Context) error          { return errPingRefused }

func TestConnectAndPingNamesThePingStep(t *testing.T) {
	t.Parallel()
	db := sql.OpenDB(pingFailConnector{})
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	path := filepath.Join(t.TempDir(), "present.db")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := connectAndPing(t.Context(), db, path)
	if !errors.Is(err, errPingRefused) {
		t.Fatalf("connectAndPing = %v, want the ping error", err)
	}
	if !strings.Contains(err.Error(), "db/sqlite: ping "+path) ||
		!strings.Contains(err.Error(), "(database file 5 bytes, wal file present)") {
		t.Fatalf("error does not name the ping step and disk state: %v", err)
	}
}

type pingFailConnector struct{}

func (pingFailConnector) Connect(context.Context) (driver.Conn, error) { return pingFailConn{}, nil }
func (pingFailConnector) Driver() driver.Driver                        { return pingFailDriver{} }

func TestDiskStateMemory(t *testing.T) {
	t.Parallel()
	if got := diskState(MemoryPath); got != "in-memory database" {
		t.Fatalf("diskState(memory) = %q", got)
	}
}

// lateConnector ends ctx while connecting, like an open the driver cannot cancel; ending it
// here rather than on a timer keeps database/sql from failing before Connect runs.
type lateConnector struct{ cancel context.CancelFunc }

func (c lateConnector) Connect(context.Context) (driver.Conn, error) {
	c.cancel()
	return pingFailConn{}, nil
}
func (lateConnector) Driver() driver.Driver { return pingFailDriver{} }

func TestConnectAndPingNamesALateConnect(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db := sql.OpenDB(lateConnector{cancel: cancel})
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	path := filepath.Join(t.TempDir(), "absent.db")
	err := connectAndPing(ctx, db, path)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("connectAndPing = %v, want the context error", err)
	}
	if !strings.Contains(err.Error(), "db/sqlite: connect "+path+" finished after the open deadline") ||
		strings.Contains(err.Error(), "ping") {
		t.Fatalf("error does not name the late connect: %v", err)
	}
}

func TestConnectAndPingWithinDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	db := sql.OpenDB(lateConnector{cancel: cancel})
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	cancel()
	path := filepath.Join(t.TempDir(), "x.db")
	err := connectAndPing(ctx, db, path)
	if !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), "db/sqlite: connect "+path+": ") {
		t.Fatalf("connectAndPing with a done ctx = %v, want the plain connect error", err)
	}
}
