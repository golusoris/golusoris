// Package webrtc is a thin helper around [pion/webrtc] for one-shot
// SDP offer/answer exchange over HTTP. Suitable for WHIP-style
// ingestion and browser → server data-channel sessions.
//
// Usage:
//
//	s := webrtc.NewSignaler(webrtc.Options{
//	    ICEServers: []pionwebrtc.ICEServer{
//	        {URLs: []string{"stun:stun.l.google.com:19302"}},
//	    },
//	    OnConnect: func(pc *pionwebrtc.PeerConnection) {
//	        pc.OnDataChannel(func(dc *pionwebrtc.DataChannel) { /* … */ })
//	    },
//	})
//	mux.Handle("/whip", s.Handler())
//
// The handler performs the "one-shot" (non-trickle) exchange: it
// waits for ICE gathering to complete before returning the SDP answer.
// Trickle-ICE clients should fall back to this flow when negotiating
// with a non-WebSocket signaler.
package webrtc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	pionwebrtc "github.com/pion/webrtc/v4"
)

// DefaultMaxBodyBytes caps incoming SDP offers. Real offers are
// typically < 10 KiB; this cap is generous.
const DefaultMaxBodyBytes int64 = 64 << 10

// Options configures a [Signaler].
type Options struct {
	// ICEServers is the ICE configuration (STUN/TURN) advertised to
	// the peer. Empty means host candidates only (same-network only).
	ICEServers []pionwebrtc.ICEServer
	// OnConnect is called once per negotiated PeerConnection, before
	// SetRemoteDescription. Register OnTrack/OnDataChannel handlers
	// here.
	OnConnect func(*pionwebrtc.PeerConnection)
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// MaxBodyBytes caps the SDP offer size. Zero uses
	// [DefaultMaxBodyBytes].
	MaxBodyBytes int64
	// API is an optional pre-built *pionwebrtc.API. When nil a default
	// API is created (suitable for most use cases).
	API *pionwebrtc.API
}

// Signaler produces PeerConnections and answers SDP offers.
type Signaler struct {
	api          *pionwebrtc.API
	cfg          pionwebrtc.Configuration
	onConnect    func(*pionwebrtc.PeerConnection)
	logger       *slog.Logger
	maxBodyBytes int64
}

// NewSignaler returns a Signaler using the given options. The API and
// Configuration are captured once; mutating Options afterwards has no
// effect.
func NewSignaler(opts Options) *Signaler {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	api := opts.API
	if api == nil {
		api = pionwebrtc.NewAPI()
	}
	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	return &Signaler{
		api:          api,
		cfg:          pionwebrtc.Configuration{ICEServers: opts.ICEServers},
		onConnect:    opts.OnConnect,
		logger:       logger,
		maxBodyBytes: maxBody,
	}
}

// Answer performs the offer → answer exchange. The returned SDP is the
// complete answer after ICE gathering; send it back to the peer as the
// response body with Content-Type: application/sdp.
//
// Ownership of pc is transferred to the caller on success — close it
// when the session ends (usually via pc.OnConnectionStateChange
// watching for Failed/Disconnected/Closed).
func (s *Signaler) Answer(ctx context.Context, offerSDP string) (answerSDP string, pc *pionwebrtc.PeerConnection, err error) {
	pc, err = s.api.NewPeerConnection(s.cfg)
	if err != nil {
		return "", nil, fmt.Errorf("webrtc: new peer connection: %w", err)
	}
	// On any early failure, close the PC so the caller doesn't leak it.
	defer func() {
		if err != nil {
			_ = pc.Close()
			pc = nil
		}
	}()

	if s.onConnect != nil {
		s.onConnect(pc)
	}

	offer := pionwebrtc.SessionDescription{Type: pionwebrtc.SDPTypeOffer, SDP: offerSDP}
	if err = pc.SetRemoteDescription(offer); err != nil {
		return "", nil, fmt.Errorf("webrtc: set remote description: %w", err)
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return "", nil, fmt.Errorf("webrtc: create answer: %w", err)
	}
	gatherComplete := pionwebrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(answer); err != nil {
		return "", nil, fmt.Errorf("webrtc: set local description: %w", err)
	}
	select {
	case <-gatherComplete:
	case <-ctx.Done():
		return "", nil, fmt.Errorf("webrtc: ice gather: %w", ctx.Err())
	}
	local := pc.LocalDescription()
	if local == nil {
		return "", nil, errors.New("webrtc: empty local description after gather")
	}
	return local.SDP, pc, nil
}

// Handler returns an http.Handler implementing the one-shot SDP
// exchange. POST with Content-Type: application/sdp, body = offer
// SDP. Returns 201 Created with the answer SDP.
func (s *Signaler) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "" && ct != "application/sdp" {
			http.Error(w, "expected Content-Type application/sdp", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
		defer func() { _ = r.Body.Close() }()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read offer: "+err.Error(), http.StatusBadRequest)
			return
		}
		if len(body) == 0 {
			http.Error(w, "empty offer", http.StatusBadRequest)
			return
		}
		answer, pc, err := s.Answer(r.Context(), string(body))
		if err != nil {
			s.logger.WarnContext(r.Context(), "webrtc: answer failed", slog.String("error", err.Error()))
			http.Error(w, "negotiation failed", http.StatusBadRequest)
			return
		}
		// Close the PC when it fails — caller tracks data channels /
		// tracks via OnConnect; there's no explicit teardown URL in the
		// one-shot handler.
		pc.OnConnectionStateChange(func(state pionwebrtc.PeerConnectionState) {
			if state == pionwebrtc.PeerConnectionStateFailed ||
				state == pionwebrtc.PeerConnectionStateClosed ||
				state == pionwebrtc.PeerConnectionStateDisconnected {
				_ = pc.Close()
			}
		})

		w.Header().Set("Content-Type", "application/sdp")
		w.WriteHeader(http.StatusCreated)
		if _, werr := w.Write([]byte(answer)); werr != nil {
			s.logger.WarnContext(r.Context(), "webrtc: write answer", slog.String("error", werr.Error()))
		}
	})
}
