// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from SnatchArr api/internal/store/storetest at 6111de5a.

package pg

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestLingerReaper(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"positive: unset gets the linger", nil, ryukLinger.String()},
		{"negative: the short name wins", map[string]string{ryukEnvShort: "30s"}, ""},
		{"boundary: a set long name is kept, however short", map[string]string{ryukEnvLong: "1s"}, "1s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range [...]string{ryukEnvShort, ryukEnvLong} {
				t.Setenv(k, tt.env[k])
			}
			if err := lingerReaper(); err != nil {
				t.Fatalf("lingerReaper: %v", err)
			}
			if got := os.Getenv(ryukEnvLong); got != tt.want {
				t.Errorf("%s = %q, want %q", ryukEnvLong, got, tt.want)
			}
		})
	}
}

func TestCheckSharedReady_Uninitialized(t *testing.T) {
	t.Parallel()
	err := checkSharedReadyState(nil, nil)
	if err == nil || !strings.Contains(err.Error(), "TestMain must call pg.Main") {
		t.Fatalf("expected uninitialized error, got %v", err)
	}
}

func TestCheckSharedReady_ErrorSet(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("boot failure")
	err := checkSharedReadyState(nil, sentinel)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped sentinel error, got %v", err)
	}
}

func TestExitStatus(t *testing.T) {
	t.Parallel()
	startErr := errors.New("boot failure")
	tests := []struct {
		name     string
		code     int
		startErr error
		wantCode int
		wantErr  bool
	}{
		{
			name:     "positive: start error with passing tests returns 1 and logs cause",
			code:     0,
			startErr: startErr,
			wantCode: 1,
			wantErr:  true,
		},
		{
			name:     "negative: nil error returns original code and logs nothing",
			code:     0,
			startErr: nil,
			wantCode: 0,
			wantErr:  false,
		},
		{
			name:     "boundary: start error preserves failure exit code and logs cause",
			code:     2,
			startErr: startErr,
			wantCode: 2,
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, out := exitStatus(tt.code, tt.startErr)
			if got != tt.wantCode {
				t.Errorf("exitStatus = %d, want %d", got, tt.wantCode)
			}
			if !tt.wantErr {
				if out != "" {
					t.Errorf("exitStatus output = %q, want empty", out)
				}
				return
			}
			if !strings.Contains(out, "start shared container") || !strings.Contains(out, startErr.Error()) {
				t.Errorf("exitStatus output = %q, want container error", out)
			}
		})
	}
}
