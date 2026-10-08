// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package webrtc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pionwebrtc "github.com/pion/webrtc/v4"
)

func TestNewSignalerCapturesICEServerConfiguration(t *testing.T) {
	t.Parallel()
	servers := []pionwebrtc.ICEServer{{
		URLs:       []string{"stun:one.example"},
		Username:   "before",
		Credential: "secret",
	}}
	s := NewSignaler(Options{ICEServers: servers})

	servers[0].URLs[0] = "stun:two.example"
	servers[0].Username = "after"

	if got := s.cfg.ICEServers[0].URLs[0]; got != "stun:one.example" {
		t.Fatalf("captured ICE URL = %q, want original value", got)
	}
	if got := s.cfg.ICEServers[0].Username; got != "before" {
		t.Fatalf("captured ICE username = %q, want original value", got)
	}
}

func TestValidateOfferRequestAcceptsCaseInsensitiveMediaType(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/whip", strings.NewReader("v=0\r\n"))
	req.Header.Set("Content-Type", "Application/SDP")

	if !validateOfferRequest(rec, req) {
		t.Fatalf("validateOfferRequest() = false, status = %d", rec.Code)
	}
}

// TestIsTerminalConnectionState_terminalStates covers every state that
// should trigger teardown.
func TestIsTerminalConnectionState_terminalStates(t *testing.T) {
	t.Parallel()
	for _, state := range []pionwebrtc.PeerConnectionState{
		pionwebrtc.PeerConnectionStateFailed,
		pionwebrtc.PeerConnectionStateClosed,
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
		pionwebrtc.PeerConnectionStateDisconnected,
	} {
		if isTerminalConnectionState(state) {
			t.Errorf("isTerminalConnectionState(%s) = true, want false", state)
		}
	}
}
