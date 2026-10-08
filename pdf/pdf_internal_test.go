// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pdf

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeChromeEnv makes this test binary act as a Chrome that never serves DevTools; a script has no Windows form.
const fakeChromeEnv = "GOLUSORIS_PDF_FAKE_CHROME"

const (
	fakeChromeHang       = "hang"
	fakeChromeExit       = "exit"
	fakeChromeMute       = "mute"
	fakeChromeDiagnostic = "fake chrome: profile still loading"
	fakeChromeHangLimit  = 30 * time.Second
	fakeLaunchTimeout    = 2 * time.Second
	// chromedpDialDefault is chromedp v0.16.0's NewBrowser dial timeout (browser.go).
	chromedpDialDefault = 10 * time.Second
)

func TestMain(m *testing.M) {
	switch os.Getenv(fakeChromeEnv) {
	case fakeChromeHang:
		_, _ = os.Stderr.WriteString(fakeChromeDiagnostic + "\n")
		time.Sleep(fakeChromeHangLimit)
		os.Exit(3)
	case fakeChromeExit:
		_, _ = os.Stderr.WriteString(fakeChromeDiagnostic + "\n")
		os.Exit(1)
	case fakeChromeMute:
		muteDevTools()
	}
	os.Exit(m.Run())
}

// muteDevTools announces a DevTools endpoint whose listener never completes the
// websocket handshake, like a cold Chrome that printed its URL but is still paging in.
func muteDevTools() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(4)
	}
	_, _ = os.Stderr.WriteString("DevTools listening on ws://" + listener.Addr().String() + "/devtools/browser/fake\n")
	time.Sleep(fakeChromeHangLimit)
	os.Exit(3)
}

func TestOptionsWithDefaultsSeparatesLaunchFromRenderTimeout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		in         Options
		wantRender time.Duration
		wantLaunch time.Duration
	}{
		{name: "zero", wantRender: defaultRenderTimeout, wantLaunch: minLaunchTimeout},
		{name: "short render keeps cold-start budget", in: Options{Timeout: 5 * time.Second}, wantRender: 5 * time.Second, wantLaunch: minLaunchTimeout},
		{name: "render equal to floor", in: Options{Timeout: minLaunchTimeout}, wantRender: minLaunchTimeout, wantLaunch: minLaunchTimeout},
		{name: "long render never shortens launch", in: Options{Timeout: 2 * time.Minute}, wantRender: 2 * time.Minute, wantLaunch: 2 * time.Minute},
		{name: "explicit launch", in: Options{Timeout: time.Minute, LaunchTimeout: time.Second}, wantRender: time.Minute, wantLaunch: time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.in.withDefaults()
			if got.Timeout != tt.wantRender || got.LaunchTimeout != tt.wantLaunch {
				t.Fatalf("withDefaults() = render %s launch %s; want %s / %s",
					got.Timeout, got.LaunchTimeout, tt.wantRender, tt.wantLaunch)
			}
		})
	}
}

func TestOutputTailKeepsMostRecentBytes(t *testing.T) {
	t.Parallel()
	full := strings.Repeat("a", chromeOutputLimit)
	ordered := strings.Repeat("0123456789abcdef", chromeOutputLimit/16)
	tests := []struct {
		name   string
		writes []string
		want   string
	}{
		{name: "empty", want: ""},
		{name: "partial", writes: []string{"first\n", "second\n"}, want: "first\nsecond"},
		{name: "exactly full", writes: []string{full}, want: full},
		{name: "wraps by one", writes: []string{full, "b"}, want: full[1:] + "b"},
		{name: "write straddles end", writes: []string{"xyz", ordered}, want: ordered},
		{name: "oversized write", writes: []string{"zz" + full}, want: full},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var tail outputTail
			for _, w := range tt.writes {
				if n, err := tail.Write([]byte(w)); n != len(w) || err != nil {
					t.Fatalf("Write() = %d, %v; want %d, nil", n, err, len(w))
				}
			}
			if got := tail.String(); got != tt.want {
				t.Fatalf("String() len %d ends %q; want len %d ending %q",
					len(got), got[max(0, len(got)-40):], len(tt.want), tt.want[max(0, len(tt.want)-40):])
			}
		})
	}
}

func TestLaunchErrorCarriesChromeOutputOnce(t *testing.T) {
	t.Parallel()
	cause := errors.New("chrome failed to start: no usable sandbox")
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "silent", want: "pdf: launch chrome: chrome failed to start: no usable sandbox; chrome printed no output"},
		{name: "already reported", output: "no usable sandbox", want: "pdf: launch chrome: chrome failed to start: no usable sandbox"},
		{name: "appended", output: "Trace/breakpoint trap", want: "pdf: launch chrome: chrome failed to start: no usable sandbox; chrome output: Trace/breakpoint trap"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := launchError(cause, tt.output)
			if !errors.Is(err, cause) || err.Error() != tt.want {
				t.Fatalf("launchError() = %q; want %q wrapping the cause", err, tt.want)
			}
		})
	}
}

func TestNewRendererLaunchTimeoutReportsChromeOutput(t *testing.T) {
	t.Setenv(fakeChromeEnv, fakeChromeHang)
	renderer, err := NewRenderer(Options{ChromePath: testExecutable(t), LaunchTimeout: fakeLaunchTimeout})
	if err == nil {
		renderer.Close()
		t.Fatal("NewRenderer() = nil error for a Chrome that never serves DevTools")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("NewRenderer() error = %v; want context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), fakeChromeDiagnostic) {
		t.Fatalf("NewRenderer() error = %v; want Chrome output %q", err, fakeChromeDiagnostic)
	}
}

func TestNewRendererReportsChromeExitOutputOnce(t *testing.T) {
	t.Setenv(fakeChromeEnv, fakeChromeExit)
	renderer, err := NewRenderer(Options{ChromePath: testExecutable(t), LaunchTimeout: fakeLaunchTimeout})
	if err == nil {
		renderer.Close()
		t.Fatal("NewRenderer() = nil error for a Chrome that exits")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("NewRenderer() error = %v; want the exit, not the deadline", err)
	}
	if got := strings.Count(err.Error(), fakeChromeDiagnostic); got != 1 {
		t.Fatalf("NewRenderer() error = %v; Chrome output appears %d times, want 1", err, got)
	}
}

func TestNewRendererLaunchTimeoutOwnsWebsocketDial(t *testing.T) {
	if testing.Short() {
		t.Skip("must outlast chromedp's 10s default dial timeout")
	}
	t.Setenv(fakeChromeEnv, fakeChromeMute)
	launch := chromedpDialDefault + time.Second
	renderer, err := NewRenderer(Options{ChromePath: testExecutable(t), LaunchTimeout: launch})
	if err == nil {
		renderer.Close()
		t.Fatal("NewRenderer() = nil error for a DevTools endpoint that never answers")
	}
	if want := "context deadline exceeded after " + launch.String(); !strings.Contains(err.Error(), want) {
		t.Fatalf("NewRenderer() error = %v; want the %s launch deadline to end the dial (%q)", err, launch, want)
	}
}

// testExecutable is this test binary, which TestMain turns into a Chrome stand-in under fakeChromeEnv.
func testExecutable(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable(): %v", err)
	}
	return self
}

func TestChromeExecutablePathPrecedence(t *testing.T) {
	tests := []struct {
		name        string
		option      string
		chromeEnv   string
		chromiumEnv string
		want        string
	}{
		{name: "option", option: "/option/chrome", chromeEnv: "/env/chrome", chromiumEnv: "/env/chromium", want: "/option/chrome"},
		{name: "chrome env", chromeEnv: "/env/chrome", chromiumEnv: "/env/chromium", want: "/env/chrome"},
		{name: "chromium env", chromiumEnv: "/env/chromium", want: "/env/chromium"},
		{name: "path fallback", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CHROME_PATH", tt.chromeEnv)
			t.Setenv("CHROMIUM_PATH", tt.chromiumEnv)
			got := chromeExecutablePath(Options{ChromePath: tt.option})
			if got != tt.want {
				t.Fatalf("chromeExecutablePath() = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestTaskContextHonorsCallerCancellation(t *testing.T) {
	t.Parallel()
	r := &Renderer{ctx: context.Background(), opts: Options{Timeout: time.Minute}}
	caller, cancelCaller := context.WithCancel(context.Background())
	task, release := r.taskContext(caller)
	t.Cleanup(release)

	cancelCaller()
	<-task.Done()
	if !errors.Is(task.Err(), context.Canceled) {
		t.Fatalf("task.Err() = %v, want context.Canceled", task.Err())
	}
}

func TestTaskContextHonorsRendererCancellation(t *testing.T) {
	t.Parallel()
	session, cancelSession := context.WithCancel(context.Background())
	r := &Renderer{ctx: session, opts: Options{Timeout: time.Minute}}
	task, release := r.taskContext(context.Background())
	t.Cleanup(release)

	cancelSession()
	<-task.Done()
	if !errors.Is(task.Err(), context.Canceled) {
		t.Fatalf("task.Err() = %v, want context.Canceled", task.Err())
	}
}
