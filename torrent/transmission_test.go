package torrent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	transmissionrpc "github.com/hekmon/transmissionrpc/v3"
)

// trRequest is the Transmission RPC request envelope.
type trRequest struct {
	Method    string          `json:"method"`
	Arguments json.RawMessage `json:"arguments"`
	Tag       int             `json:"tag"`
}

// trArgs builds a success response body echoing the request tag, as the
// transmissionrpc library requires.
func trArgs(tag int, args any) []byte {
	body, _ := json.Marshal(map[string]any{
		"result":    "success",
		"tag":       tag,
		"arguments": args,
	})
	return body
}

// newTransmissionServer returns an httptest server mimicking the Transmission
// RPC API, including the X-Transmission-Session-Id (409) CSRF handshake on the
// first request. handler receives the decoded method + args and returns the
// "arguments" object for the success envelope.
func newTransmissionServer(t *testing.T, handler func(method string, args json.RawMessage) any) *httptest.Server {
	t.Helper()
	const sessionID = "test-session-id"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") != sessionID {
			w.Header().Set("X-Transmission-Session-Id", sessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var req trRequest
		if uErr := json.Unmarshal(body, &req); uErr != nil {
			t.Errorf("decode request: %v", uErr)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(trArgs(req.Tag, handler(req.Method, req.Arguments)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// trTorrentJSON is one torrent in the torrent-get response. totalSize is in
// bytes, matching the Transmission RPC wire format.
func trTorrentJSON(id int64, hash, name string, status int, totalSizeBytes int64, pct, ratio float64) map[string]any {
	return map[string]any{
		"id":           id,
		"hashString":   hash,
		"name":         name,
		"status":       status,
		"totalSize":    totalSizeBytes,
		"sizeWhenDone": totalSizeBytes,
		"percentDone":  pct,
		"uploadRatio":  ratio,
		"rateDownload": 1024,
		"rateUpload":   512,
		"addedDate":    1700000000,
	}
}

func newTestTransmission(t *testing.T, url string) *transmissionBackend {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	c, err := newTransmissionClient(Options{
		Backend:      backendTransmission,
		Timeout:      5 * time.Second,
		Transmission: TransmissionOptions{URL: url},
	}, logger)
	if err != nil {
		t.Fatalf("newTransmissionClient: %v", err)
	}
	return c
}

func TestTransmission_AddListGetPauseResumeRemoveStats(t *testing.T) {
	t.Parallel()
	const hash = "abcdef0123456789abcdef0123456789abcdef01"
	var lastMethod string
	srv := newTransmissionServer(t, func(method string, _ json.RawMessage) any {
		lastMethod = method
		switch method {
		case "torrent-add":
			return map[string]any{
				"torrent-added": map[string]any{"id": 1, "hashString": hash, "name": "ubuntu.iso"},
			}
		case "torrent-get":
			return map[string]any{
				"torrents": []map[string]any{
					trTorrentJSON(1, hash, "ubuntu.iso", 6, 1000, 1.0, 2.5),
				},
			}
		case "session-stats":
			return map[string]any{"torrentCount": 1, "downloadSpeed": 2048, "uploadSpeed": 1024}
		default: // torrent-remove, torrent-start, torrent-stop
			return map[string]any{}
		}
	})
	c := newTestTransmission(t, srv.URL)
	ctx := context.Background()

	gotHash, err := c.Add(ctx, "magnet:?xt=urn:btih:"+hash, AddOptions{Paused: true, Label: "iso"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if gotHash != hash {
		t.Errorf("Add hash = %q, want %q", gotHash, hash)
	}

	list, err := c.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List len = %d, want 1", len(list))
	}
	got := list[0]
	if got.Hash != hash || got.Name != "ubuntu.iso" {
		t.Errorf("List[0] = %+v", got)
	}
	if got.State != StateSeeding {
		t.Errorf("State = %q, want seeding", got.State)
	}
	if got.SizeBytes != 1000 {
		t.Errorf("SizeBytes = %d, want 1000", got.SizeBytes)
	}
	if got.Ratio != 2.5 {
		t.Errorf("Ratio = %v, want 2.5", got.Ratio)
	}

	one, err := c.Get(ctx, hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if one.Hash != hash {
		t.Errorf("Get hash = %q", one.Hash)
	}

	if err = c.Pause(ctx, hash); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if lastMethod != "torrent-stop" {
		t.Errorf("Pause method = %q, want torrent-stop", lastMethod)
	}
	if err = c.Resume(ctx, hash); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if lastMethod != "torrent-start" {
		t.Errorf("Resume method = %q, want torrent-start", lastMethod)
	}

	stats, err := c.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TorrentCount != 1 || stats.DownloadRate != 2048 || stats.UploadRate != 1024 {
		t.Errorf("Stats = %+v", stats)
	}

	if err = c.Remove(ctx, hash, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

func TestTransmission_AddFile(t *testing.T) {
	t.Parallel()
	const hash = "1111111111111111111111111111111111111111"
	var sawMetaInfo bool
	srv := newTransmissionServer(t, func(method string, args json.RawMessage) any {
		if method == "torrent-add" {
			var payload struct {
				MetaInfo *string `json:"metainfo"`
			}
			_ = json.Unmarshal(args, &payload)
			sawMetaInfo = payload.MetaInfo != nil && *payload.MetaInfo != ""
			return map[string]any{"torrent-added": map[string]any{"id": 2, "hashString": hash, "name": "x"}}
		}
		return map[string]any{}
	})
	c := newTestTransmission(t, srv.URL)

	gotHash, err := c.AddFile(context.Background(), []byte("d4:infod6:lengthi1eee"), AddOptions{})
	if err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	if gotHash != hash {
		t.Errorf("AddFile hash = %q, want %q", gotHash, hash)
	}
	if !sawMetaInfo {
		t.Error("expected base64 metainfo in torrent-add payload")
	}
}

func TestTransmission_RemoveMissingIsNoError(t *testing.T) {
	t.Parallel()
	srv := newTransmissionServer(t, func(method string, _ json.RawMessage) any {
		if method == "torrent-get" {
			return map[string]any{"torrents": []map[string]any{}} // hash unknown
		}
		return map[string]any{}
	})
	c := newTestTransmission(t, srv.URL)

	// Removing a torrent the daemon does not know must not be an error.
	if err := c.Remove(context.Background(), "deadbeef", false); err != nil {
		t.Fatalf("Remove(missing) = %v, want nil", err)
	}
}

func TestTransmission_GetNotFound(t *testing.T) {
	t.Parallel()
	srv := newTransmissionServer(t, func(method string, _ json.RawMessage) any {
		if method == "torrent-get" {
			return map[string]any{"torrents": []map[string]any{}}
		}
		return map[string]any{}
	})
	c := newTestTransmission(t, srv.URL)

	_, err := c.Get(context.Background(), "deadbeef")
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
}

func TestMapTransmissionState(t *testing.T) {
	t.Parallel()
	statuses := []struct {
		code int64
		want State
	}{
		{0, StatePaused},
		{1, StateChecking},
		{2, StateChecking},
		{3, StateQueued},
		{4, StateDownloading},
		{5, StateQueued},
		{6, StateSeeding},
		{7, StateError},
		{99, StateUnknown},
	}
	for _, tc := range statuses {
		ts := transmissionrpc.TorrentStatus(tc.code)
		if got := mapTransmissionState(&ts); got != tc.want {
			t.Errorf("status %d => %q, want %q", tc.code, got, tc.want)
		}
	}
	if got := mapTransmissionState(nil); got != StateUnknown {
		t.Errorf("nil status => %q, want unknown", got)
	}
}
