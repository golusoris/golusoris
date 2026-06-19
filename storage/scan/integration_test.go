//go:build integration

package scan_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/storage/scan"
)

// clamavImage is the _base variant (~75MB, no baked DB). The wait strategy
// allows a generous deadline because freshclam/DB load is slow.
const clamavImage = "clamav/clamav:1.5_base-debian"

// startClamd boots a real clamd container and returns its host:port address.
// Skips cleanly when Docker is unavailable.
func startClamd(t *testing.T) string {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)

	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        clamavImage,
			ExposedPorts: []string{"3310/tcp"},
			WaitingFor: wait.ForAll(
				wait.ForListeningPort("3310/tcp"),
				wait.ForLog("clamd.*started").AsRegexp(),
			).WithDeadline(4 * time.Minute),
		},
		Started: true,
	}
	container, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		t.Fatalf("storage/scan: start clamav container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("storage/scan: container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "3310/tcp")
	if err != nil {
		t.Fatalf("storage/scan: mapped port: %v", err)
	}
	return net.JoinHostPort(host, port.Port())
}

func newRealScanner(t *testing.T, addr string, maxSize int64) scan.Scanner {
	t.Helper()
	s, err := scan.NewClamdScanner(scan.ClamdOptions{
		Network:    "tcp",
		Address:    addr,
		CmdTimeout: 60 * time.Second,
		MaxSize:    maxSize,
	}, slog.New(slog.DiscardHandler), clock.NewFake())
	if err != nil {
		t.Fatalf("NewClamdScanner: %v", err)
	}
	return s
}

func TestIntegration_PingAndScan(t *testing.T) {
	t.Parallel()
	addr := startClamd(t)
	s := newRealScanner(t, addr, 0)
	ctx := context.Background()

	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	// EICAR is the standard, safe AV test vector — must be detected.
	v, err := s.Scan(ctx, strings.NewReader(eicar))
	if err != nil {
		t.Fatalf("Scan(eicar): %v", err)
	}
	if v.Clean {
		t.Fatal("Scan(eicar) reported Clean, want infected")
	}
	if !strings.Contains(v.Signature, "Eicar") {
		t.Fatalf("Scan(eicar) signature = %q, want to contain Eicar", v.Signature)
	}

	// Random clean bytes must scan clean.
	clean, err := s.Scan(ctx, strings.NewReader("the quick brown fox jumps over the lazy dog"))
	if err != nil {
		t.Fatalf("Scan(clean): %v", err)
	}
	if !clean.Clean {
		t.Fatalf("Scan(clean) = %+v, want Clean", clean)
	}
}

func TestIntegration_Unavailable(t *testing.T) {
	t.Parallel()
	testcontainers.SkipIfProviderIsNotHealthy(t)
	// A closed port: nothing is listening.
	s := newRealScanner(t, "127.0.0.1:1", 0)
	_, err := s.Scan(context.Background(), strings.NewReader("x"))
	if !errors.Is(err, scan.ErrUnavailable) {
		t.Fatalf("Scan(closed) = %v, want ErrUnavailable", err)
	}
}

func TestIntegration_TooLarge(t *testing.T) {
	t.Parallel()
	addr := startClamd(t)
	s := newRealScanner(t, addr, 4) // 4-byte client-side cap

	_, err := s.Scan(context.Background(), strings.NewReader("more than four bytes"))
	if !errors.Is(err, scan.ErrTooLarge) {
		t.Fatalf("Scan(oversize) = %v, want ErrTooLarge", err)
	}
}
