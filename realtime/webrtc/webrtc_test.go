package webrtc_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pionwebrtc "github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/realtime/webrtc"
)

func TestSignaler_Answer_dataChannelRoundtrip(t *testing.T) {
	t.Parallel()
	received := make(chan string, 1)
	s := webrtc.NewSignaler(webrtc.Options{
		OnConnect: func(pc *pionwebrtc.PeerConnection) {
			pc.OnDataChannel(func(dc *pionwebrtc.DataChannel) {
				dc.OnMessage(func(msg pionwebrtc.DataChannelMessage) {
					received <- string(msg.Data)
				})
			})
		},
	})

	// Build a browser-side offerer with a pre-negotiated data channel.
	offerPC, err := pionwebrtc.NewPeerConnection(pionwebrtc.Configuration{})
	require.NoError(t, err)
	defer func() { _ = offerPC.Close() }()

	dc, err := offerPC.CreateDataChannel("chat", nil)
	require.NoError(t, err)

	open := make(chan struct{})
	dc.OnOpen(func() { close(open) })

	offer, err := offerPC.CreateOffer(nil)
	require.NoError(t, err)
	gather := pionwebrtc.GatheringCompletePromise(offerPC)
	require.NoError(t, offerPC.SetLocalDescription(offer))
	<-gather

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	answer, answerPC, err := s.Answer(ctx, offerPC.LocalDescription().SDP)
	require.NoError(t, err)
	defer func() { _ = answerPC.Close() }()

	require.NoError(t, offerPC.SetRemoteDescription(pionwebrtc.SessionDescription{
		Type: pionwebrtc.SDPTypeAnswer,
		SDP:  answer,
	}))

	select {
	case <-open:
	case <-time.After(5 * time.Second):
		t.Fatal("datachannel never opened")
	}

	require.NoError(t, dc.SendText("hello webrtc"))
	select {
	case got := <-received:
		require.Equal(t, "hello webrtc", got)
	case <-time.After(5 * time.Second):
		t.Fatal("message never arrived")
	}
}

func TestSignaler_Handler_methodNotAllowed(t *testing.T) {
	t.Parallel()
	s := webrtc.NewSignaler(webrtc.Options{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/whip", http.NoBody)
	s.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestSignaler_Handler_rejectsWrongContentType(t *testing.T) {
	t.Parallel()
	s := webrtc.NewSignaler(webrtc.Options{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/whip", strings.NewReader("x"))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
}

func TestSignaler_Handler_rejectsEmptyBody(t *testing.T) {
	t.Parallel()
	s := webrtc.NewSignaler(webrtc.Options{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/whip", http.NoBody)
	req.Header.Set("Content-Type", "application/sdp")
	s.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSignaler_Handler_endToEnd(t *testing.T) {
	t.Parallel()
	// Spin up the Signaler's HTTP handler, POST an offer, validate the
	// answer is valid SDP.
	got := make(chan struct{}, 1)
	var once sync.Once
	s := webrtc.NewSignaler(webrtc.Options{
		OnConnect: func(pc *pionwebrtc.PeerConnection) {
			pc.OnDataChannel(func(*pionwebrtc.DataChannel) {
				once.Do(func() { got <- struct{}{} })
			})
		},
	})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	offerPC, err := pionwebrtc.NewPeerConnection(pionwebrtc.Configuration{})
	require.NoError(t, err)
	defer func() { _ = offerPC.Close() }()
	_, err = offerPC.CreateDataChannel("chat", nil)
	require.NoError(t, err)
	offer, err := offerPC.CreateOffer(nil)
	require.NoError(t, err)
	gather := pionwebrtc.GatheringCompletePromise(offerPC)
	require.NoError(t, offerPC.SetLocalDescription(offer))
	<-gather

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader(offerPC.LocalDescription().SDP))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/sdp")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.Equal(t, "application/sdp", resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "v=0")
}
