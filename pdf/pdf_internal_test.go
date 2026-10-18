// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pdf

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
)

// fakeChromeEnv makes this test binary act as a Chrome that never serves DevTools; a script has no Windows form.
const (
	fakeChromeEnv    = "GOLUSORIS_PDF_FAKE_CHROME"
	fakeChromePIDEnv = "GOLUSORIS_PDF_FAKE_CHROME_PID_FILE"
)

const (
	fakeChromeHang       = "hang"
	fakeChromeExit       = "exit"
	fakeChromePipe       = "pipe"
	fakeChromeDiagnostic = "fake chrome: profile still loading"
	fakeChromeHangLimit  = 30 * time.Second
	fakeLaunchTimeout    = 2 * time.Second
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
	case fakeChromePipe:
		mutePipeDevTools()
	}
	os.Exit(m.Run())
}

// mutePipeDevTools keeps the inherited CDP pipes open without answering commands.
func mutePipeDevTools() {
	if !slices.Contains(os.Args, "--remote-debugging-pipe") {
		_, _ = os.Stderr.WriteString("fake chrome: missing pipe transport\n")
		os.Exit(4)
	}
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--remote-debugging-port") || strings.HasPrefix(arg, "--remote-debugging-address") {
			_, _ = os.Stderr.WriteString("fake chrome: unexpected debugging port\n")
			os.Exit(4)
		}
	}
	if err := os.WriteFile(os.Getenv(fakeChromePIDEnv), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		os.Exit(4)
	}
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

func TestRenderOptionsParams(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   RenderOptions
		want page.PrintToPDFParams
	}{
		{
			name: "defaults",
			want: page.PrintToPDFParams{
				Landscape: new(false), PrintBackground: new(false), Scale: 1,
				MarginTop: new(1.0), MarginBottom: new(1.0), MarginLeft: new(1.0), MarginRight: new(1.0),
				PaperWidth: 21.0 / 2.54, PaperHeight: 29.7 / 2.54,
			},
		},
		{
			name: "custom",
			in: RenderOptions{
				Landscape: true, PrintBackground: true, Scale: 1.25,
				MarginTop: 2, MarginBottom: 3, MarginLeft: 4, MarginRight: 5,
				PaperWidth: 25.4, PaperHeight: 50.8,
			},
			want: page.PrintToPDFParams{
				Landscape: new(true), PrintBackground: new(true), Scale: 1.25,
				MarginTop: new(2.0), MarginBottom: new(3.0), MarginLeft: new(4.0), MarginRight: new(5.0),
				PaperWidth: 25.4 / 2.54, PaperHeight: 50.8 / 2.54,
			},
		},
		{
			name: "explicit zero margins and partial paper",
			in:   RenderOptions{MarginTop: 0.5, PaperWidth: 25.4},
			want: page.PrintToPDFParams{
				Landscape: new(false), PrintBackground: new(false), Scale: 1,
				MarginTop: new(0.5), MarginBottom: new(0.0), MarginLeft: new(0.0), MarginRight: new(0.0),
				PaperWidth: 25.4 / 2.54, PaperHeight: 29.7 / 2.54,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.in.params()
			if math.Abs(got.PaperWidth-tt.want.PaperWidth) > 1e-12 || math.Abs(got.PaperHeight-tt.want.PaperHeight) > 1e-12 {
				t.Errorf("paper = %g x %g; want %g x %g", got.PaperWidth, got.PaperHeight, tt.want.PaperWidth, tt.want.PaperHeight)
			}
			got.PaperWidth, got.PaperHeight = tt.want.PaperWidth, tt.want.PaperHeight
			if !reflect.DeepEqual(*got, tt.want) {
				t.Fatalf("params() = %+v; want %+v", *got, tt.want)
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
	renderer, err := NewRendererContext(t.Context(), Options{ChromePath: testExecutable(t), LaunchTimeout: fakeLaunchTimeout})
	if err == nil {
		renderer.Close()
		t.Fatal("NewRendererContext() = nil error for a Chrome that never serves DevTools")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("NewRendererContext() error = %v; want context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), fakeChromeDiagnostic) {
		t.Fatalf("NewRendererContext() error = %v; want Chrome output %q", err, fakeChromeDiagnostic)
	}
}

func TestNewRendererReportsChromeExitOutputOnce(t *testing.T) {
	t.Setenv(fakeChromeEnv, fakeChromeExit)
	renderer, err := NewRendererContext(t.Context(), Options{ChromePath: testExecutable(t), LaunchTimeout: fakeLaunchTimeout})
	if err == nil {
		renderer.Close()
		t.Fatal("NewRendererContext() = nil error for a Chrome that exits")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("NewRendererContext() error = %v; want the exit, not the deadline", err)
	}
	if got := strings.Count(err.Error(), fakeChromeDiagnostic); got != 1 {
		t.Fatalf("NewRendererContext() error = %v; Chrome output appears %d times, want 1", err, got)
	}
}

func TestNewRendererLaunchTimeoutOwnsSilentPipe(t *testing.T) {
	t.Setenv(fakeChromeEnv, fakeChromePipe)
	pidFile := filepath.Join(t.TempDir(), "chrome.pid")
	t.Setenv(fakeChromePIDEnv, pidFile)
	started := time.Now()
	renderer, err := NewRendererContext(t.Context(), Options{ChromePath: testExecutable(t), LaunchTimeout: fakeLaunchTimeout})
	elapsed := time.Since(started)
	if err == nil {
		renderer.Close()
		t.Fatal("NewRendererContext() = nil error for a CDP pipe that never answers")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("NewRendererContext() error = %v; want context.DeadlineExceeded", err)
	}
	if elapsed < fakeLaunchTimeout || elapsed > fakeLaunchTimeout+time.Second {
		t.Errorf("NewRendererContext() elapsed = %s; want [%s, %s]", elapsed, fakeLaunchTimeout, fakeLaunchTimeout+time.Second)
	}
	assertFakeChromeReaped(t, pidFile)
	assertChromeGoroutinesStopped(t)
	t.Logf("silent pipe canceled and reaped after %s", elapsed)
}

func assertFakeChromeReaped(t *testing.T, pidFile string) {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("fake Chrome did not start with pipe transport: %v", err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatalf("fake Chrome PID: %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Log("process probe unavailable: Windows does not support signal 0")
		return
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("FindProcess(%d): %v", pid, err)
	}
	defer process.Release()
	if err := process.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		t.Errorf("fake Chrome PID %d still exists or probe failed: %v", pid, err)
	}
}

func assertChromeGoroutinesStopped(t *testing.T) {
	t.Helper()
	stack := make([]byte, 1<<20)
	for range 100 {
		n := runtime.Stack(stack, true)
		if !strings.Contains(string(stack[:n]), "github.com/chromedp/chromedp") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("chromedp goroutines remain after canceled launch:\n%s", stack[:runtime.Stack(stack, true)])
}

func TestNewRendererExpiredLaunchTimeout(t *testing.T) {
	t.Setenv(fakeChromeEnv, fakeChromeHang)
	started := time.Now()
	renderer, err := NewRendererContext(t.Context(), Options{ChromePath: testExecutable(t), LaunchTimeout: -time.Nanosecond})
	if err == nil {
		renderer.Close()
		t.Fatal("NewRendererContext() = nil error for an expired launch budget")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("NewRendererContext() error = %v; want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("NewRendererContext() elapsed = %s; want at most 1s", elapsed)
	}
	assertChromeGoroutinesStopped(t)
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

func TestNewRendererContextCancelledCaller(t *testing.T) {
	t.Setenv(fakeChromeEnv, fakeChromeHang)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	renderer, err := NewRendererContext(ctx, Options{ChromePath: testExecutable(t), LaunchTimeout: fakeLaunchTimeout})
	if err == nil {
		renderer.Close()
		t.Fatal("NewRendererContext() = nil error for a cancelled caller context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("NewRendererContext() error = %v; want context.Canceled", err)
	}
}
