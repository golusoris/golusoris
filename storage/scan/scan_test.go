package scan_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/storage/scan"
)

// eicar is the standard, safe, non-malicious AV test vector. No real malware.
const eicar = `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`

func TestNoopScanner_AlwaysClean(t *testing.T) {
	t.Parallel()
	s := newNoopForTest(t)
	ctx := context.Background()

	v, err := s.Scan(ctx, strings.NewReader(eicar))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !v.Clean {
		t.Fatalf("noop verdict = %+v, want Clean", v)
	}
	if err := s.ScanStrict(ctx, strings.NewReader("anything")); err != nil {
		t.Fatalf("ScanStrict: %v", err)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestNoopScanner_DrainError(t *testing.T) {
	t.Parallel()
	s := newNoopForTest(t)
	_, err := s.Scan(context.Background(), errReader{})
	if err == nil {
		t.Fatal("Scan(errReader) = nil, want drain error")
	}
}

// TestClamdScanner_Verdict drives the full clamd backend against a fake clamd
// TCP server speaking the INSTREAM wire protocol — no Docker, no real daemon.
func TestClamdScanner_Verdict(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		reply     string // raw clamd response line (without trailing newline)
		body      string
		wantClean bool
		wantSig   string
	}{
		{"clean", "stream: OK", "harmless bytes", true, ""},
		{"infected", "stream: Eicar-Test-Signature FOUND", eicar, false, "Eicar-Test-Signature"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := startFakeClamd(t, fakeConfig{instreamReply: tt.reply})
			s := newClamdForTest(t, srv.addr())

			v, err := s.Scan(context.Background(), strings.NewReader(tt.body))
			if err != nil {
				t.Fatalf("Scan: %v", err)
			}
			if v.Clean != tt.wantClean {
				t.Fatalf("Clean = %v, want %v (raw %q)", v.Clean, tt.wantClean, v.Raw)
			}
			if v.Signature != tt.wantSig {
				t.Fatalf("Signature = %q, want %q", v.Signature, tt.wantSig)
			}
		})
	}
}

func TestClamdScanner_ScanStrict(t *testing.T) {
	t.Parallel()

	clean := startFakeClamd(t, fakeConfig{instreamReply: "stream: OK"})
	if err := newClamdForTest(t, clean.addr()).
		ScanStrict(context.Background(), strings.NewReader("ok")); err != nil {
		t.Fatalf("ScanStrict(clean) = %v, want nil", err)
	}

	infected := startFakeClamd(t, fakeConfig{instreamReply: "stream: Eicar-Test-Signature FOUND"})
	err := newClamdForTest(t, infected.addr()).
		ScanStrict(context.Background(), strings.NewReader(eicar))
	if !errors.Is(err, scan.ErrInfected) {
		t.Fatalf("ScanStrict(infected) = %v, want ErrInfected", err)
	}
	if !strings.Contains(err.Error(), "Eicar-Test-Signature") {
		t.Fatalf("ScanStrict error missing signature: %v", err)
	}
}

func TestClamdScanner_Ping(t *testing.T) {
	t.Parallel()
	srv := startFakeClamd(t, fakeConfig{pingReply: "PONG"})
	if err := newClamdForTest(t, srv.addr()).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestClamdScanner_PingNoPong(t *testing.T) {
	t.Parallel()
	srv := startFakeClamd(t, fakeConfig{pingReply: "NOPE"})
	err := newClamdForTest(t, srv.addr()).Ping(context.Background())
	if !errors.Is(err, scan.ErrUnavailable) {
		t.Fatalf("Ping(no PONG) = %v, want ErrUnavailable", err)
	}
}

// TestClamdScanner_DaemonError feeds a clamd ERROR response (e.g. size limit
// exceeded) through the real client; the backend must classify it as
// ErrUnavailable, not a clean/infected verdict.
func TestClamdScanner_DaemonError(t *testing.T) {
	t.Parallel()
	srv := startFakeClamd(t, fakeConfig{instreamReply: "INSTREAM size limit exceeded ERROR"})
	_, err := newClamdForTest(t, srv.addr()).
		Scan(context.Background(), strings.NewReader("data"))
	if !errors.Is(err, scan.ErrUnavailable) {
		t.Fatalf("Scan(daemon ERROR) = %v, want ErrUnavailable", err)
	}
}

func TestClamdScanner_Unavailable(t *testing.T) {
	t.Parallel()
	// Dial a closed port: nothing is listening at this address.
	s := newClamdForTest(t, "127.0.0.1:1")
	_, err := s.Scan(context.Background(), strings.NewReader("x"))
	if !errors.Is(err, scan.ErrUnavailable) {
		t.Fatalf("Scan(closed) = %v, want ErrUnavailable", err)
	}
	if err := s.Ping(context.Background()); !errors.Is(err, scan.ErrUnavailable) {
		t.Fatalf("Ping(closed) = %v, want ErrUnavailable", err)
	}
}

func TestClamdScanner_TooLarge(t *testing.T) {
	t.Parallel()
	srv := startFakeClamd(t, fakeConfig{instreamReply: "stream: OK"})
	s := newClamdForTestSize(t, srv.addr(), 8) // 8-byte cap

	_, err := s.Scan(context.Background(), strings.NewReader("way more than eight bytes"))
	if !errors.Is(err, scan.ErrTooLarge) {
		t.Fatalf("Scan(oversize) = %v, want ErrTooLarge", err)
	}
}

func TestClamdScanner_AtSizeLimit(t *testing.T) {
	t.Parallel()
	srv := startFakeClamd(t, fakeConfig{instreamReply: "stream: OK"})
	s := newClamdForTestSize(t, srv.addr(), 5) // exactly the payload length

	v, err := s.Scan(context.Background(), strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Scan(at limit) = %v, want nil", err)
	}
	if !v.Clean {
		t.Fatalf("verdict = %+v, want Clean", v)
	}
}

// newClamdForTest builds a real clamd-backed Scanner pointed at addr.
func newClamdForTest(t *testing.T, addr string) scan.Scanner {
	t.Helper()
	return newClamdForTestSize(t, addr, 0)
}

func newClamdForTestSize(t *testing.T, addr string, maxSize int64) scan.Scanner {
	t.Helper()
	s, err := scan.NewClamdScanner(scan.ClamdOptions{
		Network: "tcp",
		Address: addr,
		MaxSize: maxSize,
	}, discardLogger(t), clock.NewFake())
	if err != nil {
		t.Fatalf("NewClamdScanner: %v", err)
	}
	return s
}

func newNoopForTest(t *testing.T) scan.Scanner {
	t.Helper()
	return scan.NewNoopScanner(discardLogger(t))
}

func discardLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.DiscardHandler)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

var _ io.Reader = errReader{}
