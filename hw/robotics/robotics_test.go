// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package robotics_test

import (
	"testing"

	"github.com/golusoris/golusoris/hw/robotics"
)

func TestNewRobot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		botName  string
		wantName string
	}{
		{
			name:     "positive named robot",
			botName:  "mars-rover",
			wantName: "mars-rover",
		},
		{
			name:     "boundary empty name",
			botName:  "",
			wantName: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			work := func() { called = true }

			bot := robotics.NewRobot(tc.botName, work)
			if bot == nil {
				t.Fatal("expected non-nil robot")
			}
			if bot.Name != tc.wantName {
				t.Fatalf("bot.Name = %q, want %q", bot.Name, tc.wantName)
			}
			if bot.Work == nil {
				t.Fatal("expected non-nil work function")
			}
			bot.Work()
			if !called {
				t.Fatal("expected work function to execute")
			}
		})
	}
}
