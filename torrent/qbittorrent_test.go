package torrent

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
	"go.uber.org/fx"
)

// qbitState tracks endpoint hits across a fake qBittorrent WebUI server.
type qbitState struct {
	mu       sync.Mutex
	lastPath string
	torrents []map[string]any
}

func (s *qbitState) record(path string) {
	s.mu.Lock()
	s.lastPath = path
	s.mu.Unlock()
}

// newQBittorrentServer mimics the qBittorrent WebAPI v2: cookie login,
// version, torrents/info, transfer/info, and the add/delete/start/stop
// endpoints. It reports WebAPI 2.11.0 so the new start/stop routes are used.
func newQBittorrentServer(t *testing.T, st *qbitState) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test-sid", Path: "/"})
		_, _ = w.Write([]byte("Ok."))
	})
	mux.HandleFunc("/api/v2/app/webapiVersion", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("2.11.0"))
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, _ *http.Request) {
		st.record("torrents/info")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(st.torrents)
	})
	mux.HandleFunc("/api/v2/transfer/info", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"dl_info_speed": 4096, "up_info_speed": 2048})
	})
	for _, ep := range []string{"add", "delete", "start", "stop", "pause", "resume"} {
		path := "/api/v2/torrents/" + ep
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			st.record("torrents/" + ep)
			// qBittorrent replies "Ok." as text/plain to torrents/add.
			w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
			_, _ = w.Write([]byte("Ok."))
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// bootQBittorrent wires the qbittorrent backend through fx so the OnStart login
// hook fires, and returns the populated Client.
func bootQBittorrent(t *testing.T, host string) Client {
	t.Helper()
	var got Client
	app := fx.New(
		fx.NopLogger,
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(func() Options {
			return Options{
				Backend:     backendQBittorrent,
				Timeout:     5 * time.Second,
				QBittorrent: QBittorrentOptions{Host: host, Username: "admin", Password: "pw"},
			}
		}),
		fx.Provide(newClient),
		fx.Populate(&got),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	if err := app.Start(ctx); err != nil {
		t.Fatalf("fx start (login): %v", err)
	}
	t.Cleanup(func() {
		if err := app.Stop(ctx); err != nil {
			t.Errorf("fx stop: %v", err)
		}
	})
	return got
}

func qbitTorrentJSON(hash, name, state string, size int64, progress, ratio float64) map[string]any {
	return map[string]any{
		"hash": hash, "name": name, "state": state, "size": size,
		"progress": progress, "ratio": ratio,
		"dlspeed": 1024, "upspeed": 512, "added_on": 1700000000,
	}
}

func TestQBittorrent_LoginAndCRUD(t *testing.T) {
	t.Parallel()
	const hash = "abcdef0123456789abcdef0123456789abcdef01"
	st := &qbitState{torrents: []map[string]any{
		qbitTorrentJSON(hash, "ubuntu.iso", "uploading", 2048, 1.0, 1.5),
	}}
	srv := newQBittorrentServer(t, st)
	c := bootQBittorrent(t, srv.URL)
	ctx := context.Background()

	if _, err := c.Add(ctx, "magnet:?xt=urn:btih:"+hash, AddOptions{Paused: true, Label: "iso"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	st.mu.Lock()
	addPath := st.lastPath
	st.mu.Unlock()
	if addPath != "torrents/add" {
		t.Errorf("Add hit %q, want torrents/add", addPath)
	}

	list, err := c.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List len = %d, want 1", len(list))
	}
	if list[0].State != StateSeeding {
		t.Errorf("State = %q, want seeding", list[0].State)
	}
	if list[0].SizeBytes != 2048 {
		t.Errorf("SizeBytes = %d, want 2048", list[0].SizeBytes)
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
	st.mu.Lock()
	pausePath := st.lastPath
	st.mu.Unlock()
	if pausePath != "torrents/stop" {
		t.Errorf("Pause hit %q, want torrents/stop (WebAPI 2.11)", pausePath)
	}

	if err = c.Resume(ctx, hash); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	st.mu.Lock()
	resumePath := st.lastPath
	st.mu.Unlock()
	if resumePath != "torrents/start" {
		t.Errorf("Resume hit %q, want torrents/start", resumePath)
	}

	stats, err := c.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TorrentCount != 1 || stats.DownloadRate != 4096 || stats.UploadRate != 2048 {
		t.Errorf("Stats = %+v", stats)
	}

	if err = c.Remove(ctx, hash, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	st.mu.Lock()
	delPath := st.lastPath
	st.mu.Unlock()
	if delPath != "torrents/delete" {
		t.Errorf("Remove hit %q, want torrents/delete", delPath)
	}
}

func TestQBittorrent_AddFile(t *testing.T) {
	t.Parallel()
	st := &qbitState{torrents: []map[string]any{}}
	srv := newQBittorrentServer(t, st)
	c := bootQBittorrent(t, srv.URL)

	if _, err := c.AddFile(context.Background(), []byte("d4:infod6:lengthi1eee"), AddOptions{SavePath: "/dl"}); err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	st.mu.Lock()
	got := st.lastPath
	st.mu.Unlock()
	if got != "torrents/add" {
		t.Errorf("AddFile hit %q, want torrents/add", got)
	}
}

func TestQBittorrent_GetNotFound(t *testing.T) {
	t.Parallel()
	st := &qbitState{torrents: []map[string]any{}}
	srv := newQBittorrentServer(t, st)
	c := bootQBittorrent(t, srv.URL)

	_, err := c.Get(context.Background(), "deadbeef")
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
}

func TestMapQBittorrentState(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want State
	}{
		{"error", StateError},
		{"missingFiles", StateError},
		{"pausedUP", StatePaused},
		{"stoppedDL", StatePaused},
		{"queuedUP", StateQueued},
		{"checkingDL", StateChecking},
		{"uploading", StateSeeding},
		{"stalledUP", StateSeeding},
		{"downloading", StateDownloading},
		{"metaDL", StateDownloading},
		{"moving", StateUnknown},
	}
	for _, tc := range cases {
		if got := mapQBittorrentState(qbt.TorrentState(tc.in)); got != tc.want {
			t.Errorf("state %q => %q, want %q", tc.in, got, tc.want)
		}
	}
}
