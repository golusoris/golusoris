// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package webrtc

import (
	"testing"

	pionwebrtc "github.com/pion/webrtc/v4"
)

// TestIsTerminalConnectionState_terminalStates covers every state that
// should trigger teardown.
func TestIsTerminalConnectionState_terminalStates(t *testing.T) {
	t.Parallel()
	for _, state := range []pionwebrtc.PeerConnectionState{
		pionwebrtc.PeerConnectionStateFailed,
		pionwebrtc.PeerConnectionStateClosed,
		pionwebrtc.PeerConnectionStateDisconnected,
	} {
		if !isTerminalConnectionState(state) {
			t.Errorf("isTerminalConnectionState(%s) = false, want true", state)
		}
	}
}

// TestIsTerminalConnectionState_nonTerminalStates is the boundary: the
// in-progress/steady states must not trigger teardown.
func TestIsTerminalConnectionState_nonTerminalStates(t *testing.T) {
	t.Parallel()
	for _, state := range []pionwebrtc.PeerConnectionState{
		pionwebrtc.PeerConnectionStateNew,
		pionwebrtc.PeerConnectionStateConnecting,
		pionwebrtc.PeerConnectionStateConnected,
	} {
		if isTerminalConnectionState(state) {
			t.Errorf("isTerminalConnectionState(%s) = true, want false", state)
		}
	}
}
