package mcp

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

func TestOptions_WithDefaults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   Options
		want Options
	}{
		{
			name: "empty fills every default",
			in:   Options{},
			want: Options{
				Transport: TransportStdio,
				HTTP:      HTTPOptions{Addr: defaultAddr, Path: defaultPath},
				Name:      defaultName,
				Version:   defaultVersion,
			},
		},
		{
			name: "explicit values are preserved",
			in: Options{
				Transport: TransportHTTP,
				HTTP:      HTTPOptions{Addr: ":1234", Path: "/rpc"},
				Name:      "myapp",
				Version:   "9.9.9",
			},
			want: Options{
				Transport: TransportHTTP,
				HTTP:      HTTPOptions{Addr: ":1234", Path: "/rpc"},
				Name:      "myapp",
				Version:   "9.9.9",
			},
		},
		{
			name: "partial http keeps addr, fills path",
			in:   Options{Transport: TransportHTTP, HTTP: HTTPOptions{Addr: ":2000"}},
			want: Options{
				Transport: TransportHTTP,
				HTTP:      HTTPOptions{Addr: ":2000", Path: defaultPath},
				Name:      defaultName,
				Version:   defaultVersion,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.in.withDefaults(); got != tt.want {
				t.Errorf("withDefaults() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestOptions_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		transport Transport
		wantErr   bool
	}{
		{"stdio ok", TransportStdio, false},
		{"http ok", TransportHTTP, false},
		{"empty rejected", "", true},
		{"unknown rejected", "smoke-signals", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := Options{Transport: tt.transport}.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("validate(%q) err = %v, wantErr %v", tt.transport, err, tt.wantErr)
			}
		})
	}
}

func TestLoadOptions_Defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	got, err := loadOptions(cfg)
	if err != nil {
		t.Fatalf("loadOptions: %v", err)
	}
	if got.Transport != TransportStdio || got.HTTP.Addr != defaultAddr || got.HTTP.Path != defaultPath {
		t.Errorf("defaults not applied: %+v", got)
	}
}

// TestStdoutRedirect_StrayWritesGoToSink verifies that after install, writes to
// os.Stdout are copied to the sink (stderr in prod) and never to the pinned
// real stdout handed to the transport.
//
//nolint:paralleltest // mutates the process-global os.Stdout; must run serially.
func TestStdoutRedirect_StrayWritesGoToSink(t *testing.T) {
	var sink bytes.Buffer
	red, realOut, err := installStdoutRedirect(&sink)
	if err != nil {
		t.Fatalf("installStdoutRedirect: %v", err)
	}
	if realOut == nil {
		t.Fatal("expected a non-nil pinned real stdout")
	}
	// A stray write that would corrupt JSON-RPC if it hit the transport's stdout.
	if _, err := os.Stdout.WriteString("stray-line\n"); err != nil {
		t.Fatalf("write to redirected stdout: %v", err)
	}
	if err := red.Close(); err != nil {
		t.Fatalf("redirect close: %v", err)
	}
	if got := sink.String(); got != "stray-line\n" {
		t.Errorf("sink = %q, want stray output redirected there", got)
	}
	if os.Stdout == realOut {
		// Close restores os.Stdout to the original; realOut is that original.
		return
	}
	t.Errorf("Close did not restore os.Stdout to the original")
}

// TestRunStdio_DisconnectTriggersShutdown wires the real Module with the stdio
// transport, but swaps the transport seam for an in-memory pair so a client can
// connect and disconnect. On disconnect the server's Run returns and the module
// must end the app via fx.Shutdowner (observed via app.Done()).
//
//nolint:paralleltest // overrides package-level transport/stdin seams; must run serially.
func TestRunStdio_DisconnectTriggersShutdown(t *testing.T) {
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()

	restore := swapStdioSeam(serverTransport)
	defer restore()

	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	app := fx.New(
		fx.NopLogger,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		Module,
	)
	if app.Err() != nil {
		t.Fatalf("fx.New: %v", app.Err())
	}

	startCtx, startCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer startCancel()
	if startErr := app.Start(startCtx); startErr != nil {
		t.Fatalf("app.Start: %v", startErr)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = app.Stop(stopCtx)
	})

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "c", Version: "0"}, nil)
	session, err := client.Connect(startCtx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("session close: %v", err)
	}

	select {
	case <-app.Done():
		// Shutdowner fired — the module ended the app on disconnect.
	case <-time.After(5 * time.Second):
		t.Fatal("app did not shut down after stdio client disconnect")
	}
}

// swapStdioSeam replaces the transport constructor so runStdio uses the given
// in-memory transport, and stubs stdin so the redirect install does not consume
// the real terminal. It returns a restore func.
func swapStdioSeam(transport sdkmcp.Transport) func() {
	var once sync.Once
	prevNew := newStdioTransport
	prevStdin := stdin
	newStdioTransport = func(io.ReadCloser, io.WriteCloser) sdkmcp.Transport {
		return transport
	}
	stdin = io.NopCloser(bytes.NewReader(nil))
	return func() {
		once.Do(func() {
			newStdioTransport = prevNew
			stdin = prevStdin
		})
	}
}
