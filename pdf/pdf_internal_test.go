// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pdf

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
