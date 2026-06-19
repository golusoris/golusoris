package scan

import (
	"testing"
	"time"

	"github.com/baruwa-enterprise/clamd"
)

// canned builds a single-element clamd response slice for mapResponses tests.
func canned(status, signature string) []*clamd.Response {
	return []*clamd.Response{{
		Filename:  "stream",
		Status:    status,
		Signature: signature,
		Raw:       "stream: " + signature + " " + status,
	}}
}

func TestParseSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{"empty is zero", "", 0, false},
		{"bare bytes", "1024", 1024, false},
		{"mebibytes", "25MB", 25 << 20, false},
		{"kibibytes spaced", "5 KiB", 5 << 10, false},
		{"gigabytes lower", "2gb", 2 << 30, false},
		{"bytes suffix", "512b", 512, false},
		{"fractional mb", "1.5MB", int64(1.5 * (1 << 20)), false},
		{"unparseable", "abc", 0, true},
		{"negative", "-1MB", 0, true},
		{"bad number with suffix", "xMB", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseSize(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseSize(%q) = nil error, want error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSize(%q) = %v, want nil", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("parseSize(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	if opts.Backend != backendClamd {
		t.Fatalf("default backend = %q, want %q", opts.Backend, backendClamd)
	}
	if opts.FailOpen {
		t.Fatal("default FailOpen = true, want false (fail-closed is the security default)")
	}
	if !opts.PingOnStart {
		t.Fatal("default PingOnStart = false, want true")
	}
	if opts.Network != "tcp" {
		t.Fatalf("default network = %q, want tcp", opts.Network)
	}
}

func TestClamdOptionsFrom(t *testing.T) {
	t.Parallel()
	copts, err := clamdOptionsFrom(Options{
		Address:     "10.0.0.1:3310",
		Network:     "",
		MaxSize:     "10MB",
		ConnTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("clamdOptionsFrom: %v", err)
	}
	if copts.Network != "tcp" {
		t.Fatalf("network = %q, want tcp (defaulted)", copts.Network)
	}
	if copts.MaxSize != 10<<20 {
		t.Fatalf("max_size = %d, want %d", copts.MaxSize, 10<<20)
	}
	if copts.ConnTimeout != 3*time.Second {
		t.Fatalf("conn_timeout = %v, want 3s", copts.ConnTimeout)
	}
}

func TestClamdOptionsFrom_BadSize(t *testing.T) {
	t.Parallel()
	if _, err := clamdOptionsFrom(Options{MaxSize: "notasize"}); err == nil {
		t.Fatal("clamdOptionsFrom(bad size) = nil error, want error")
	}
}

func TestMapResponses(t *testing.T) {
	t.Parallel()
	v, err := mapResponses(canned("OK", ""))
	if err != nil || !v.Clean {
		t.Fatalf("OK -> %+v, %v; want Clean", v, err)
	}
	v, err = mapResponses(canned("FOUND", "Eicar-Test-Signature"))
	if err != nil {
		t.Fatalf("FOUND err: %v", err)
	}
	if v.Clean || v.Signature != "Eicar-Test-Signature" {
		t.Fatalf("FOUND -> %+v, want infected with signature", v)
	}
	if _, err = mapResponses(nil); err == nil {
		t.Fatal("mapResponses(nil) = nil error, want ErrUnavailable")
	}
	if _, err = mapResponses(canned("ERROR", "")); err == nil {
		t.Fatal("mapResponses(ERROR status) = nil error, want error")
	}
}
