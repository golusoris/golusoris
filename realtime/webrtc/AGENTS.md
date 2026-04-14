# realtime/webrtc

Thin helper around [pion/webrtc](https://github.com/pion/webrtc) for
one-shot SDP offer/answer exchange over HTTP. Suitable for WHIP-style
ingestion and browser → server data-channel sessions.

## Surface

- `webrtc.NewSignaler(Options) *Signaler`.
- `(*Signaler).Answer(ctx, offerSDP) (answerSDP, *pion.PeerConnection, error)`.
- `(*Signaler).Handler() http.Handler` — POST `application/sdp` offer
  body, returns `201 Created` with `application/sdp` answer body.
- `Options{ICEServers, OnConnect, Logger, MaxBodyBytes, API}`.

## Design

- **One-shot (non-trickle):** `Answer` waits for ICE gathering to
  complete before returning. Good enough for HTTP-based signaling; use
  a WebSocket-based signaler elsewhere if you need trickle-ICE.
- **Ownership:** On a successful `Answer`, the caller owns the
  returned `*pion.PeerConnection`. The handler auto-closes on
  `Failed/Disconnected/Closed` transitions; bare `Answer` callers must
  wire their own teardown.
- **Registration:** set track / data-channel handlers in `OnConnect`
  *before* `SetRemoteDescription` — pion only dispatches the
  `OnDataChannel` event when handlers are registered first.

## Notes

- Body capped at 64 KiB (real offers are < 10 KiB).
- No authentication — mount behind a signed / session-gated URL.
- Pion pulls in a sizable transitive tree (DTLS, SRTP, STUN, SCTP,
  TURN). Keep this module opt-in; don't import it from a shared
  framework path.
