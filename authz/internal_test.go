// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package authz

import (
	"log/slog"
	"testing"

	fileadapter "github.com/casbin/casbin/v3/persist/file-adapter"
)

func TestNewEnforcerRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	var typedNilAdapter *fileadapter.Adapter
	tests := []struct {
		name   string
		opts   Options
		logger *slog.Logger
	}{
		{
			name:   "typed-nil adapter",
			opts:   Options{Adapter: typedNilAdapter},
			logger: slog.New(slog.DiscardHandler),
		},
		{
			name:   "nil logger",
			opts:   Options{Adapter: fileadapter.NewAdapter("")},
			logger: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := newEnforcer(test.opts, test.logger); err == nil {
				t.Fatal("expected dependency validation error")
			}
		})
	}
}
