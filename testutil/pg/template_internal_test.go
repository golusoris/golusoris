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
