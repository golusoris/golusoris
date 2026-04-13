package server_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/httpx/server"
)

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	o := server.DefaultOptions()
	if o.Addr != ":8080" {
		t.Errorf("Addr = %q", o.Addr)
	}
	if o.Timeouts.Header != 5*time.Second {
		t.Errorf("Timeouts.Header = %v", o.Timeouts.Header)
	}
	if o.Limits.Body != 10<<20 {
		t.Errorf("Limits.Body = %d", o.Limits.Body)
	}
}

func TestNewAppliesTimeouts(t *testing.T) {
	t.Parallel()
	srv := server.New(http.NotFoundHandler(), server.Options{
		Addr: ":0",
		Timeouts: server.TimeoutOptions{
			Read:   3 * time.Second,
			Header: 1 * time.Second,
		},
	})
	if srv.ReadTimeout != 3*time.Second {
		t.Errorf("ReadTimeout = %v", srv.ReadTimeout)
	}
	if srv.ReadHeaderTimeout != 1*time.Second {
		t.Errorf("ReadHeaderTimeout = %v", srv.ReadHeaderTimeout)
	}
}

// TestBodyLimitEnforced proves requests larger than Limits.Body are truncated.
func TestBodyLimitEnforced(t *testing.T) {
	t.Parallel()
	received := make(chan int, 1)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received <- len(b)
		w.WriteHeader(http.StatusOK)
	})
	srv := server.New(h, server.Options{
		Addr:   ":0",
		Limits: server.LimitOptions{Body: 10},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	url := "http://" + ln.Addr().String() + "/"
	resp, err := http.Post(url, "text/plain", strings.NewReader("aaaaaaaaaaaaaaaaaaaa"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_ = resp.Body.Close()

	select {
	case n := <-received:
		// MaxBytesReader lets at most `limit` bytes through before erroring.
		if n > 10 {
			t.Errorf("handler received %d bytes, limit is 10", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not run")
	}
}

func TestLoadOptionsFromConfig(t *testing.T) {
	t.Setenv("APP_HTTP_ADDR", ":9090")
	t.Setenv("APP_HTTP_TIMEOUTS_READ", "45s")
	t.Setenv("APP_HTTP_LIMITS_BODY", "1048576")

	cfg, err := config.New(config.Options{EnvPrefix: "APP_", Delimiter: "."})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	var opts server.Options
	if err := cfg.Unmarshal("http", &opts); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if opts.Addr != ":9090" {
		t.Errorf("Addr = %q", opts.Addr)
	}
	if opts.Timeouts.Read != 45*time.Second {
		t.Errorf("Timeouts.Read = %v", opts.Timeouts.Read)
	}
	if opts.Limits.Body != 1048576 {
		t.Errorf("Limits.Body = %d", opts.Limits.Body)
	}
}
