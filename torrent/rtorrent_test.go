package torrent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	rt "github.com/autobrr/go-rtorrent"
)

// rtMethodRe extracts the methodName from an XML-RPC request body.
var rtMethodRe = regexp.MustCompile(`<methodName>([^<]+)</methodName>`)

// rtResp wraps a single value in a methodResponse envelope.
func rtResp(valueXML string) string {
	return "<?xml version=\"1.0\"?><methodResponse><params><param><value>" +
		valueXML + "</value></param></params></methodResponse>"
}

func rtString(s string) string { return "<string>" + s + "</string>" }
func rtInt(n int) string       { return fmt.Sprintf("<i8>%d</i8>", n) }

// rtMulticallRow builds one inner array of field values in the order
// GetTorrents queries: name, size_bytes, hash, custom1(label), directory,
// is_active, complete, ratio, creation_date, finished, started.
func rtMulticallRow(name string, size int, hash, label, dir string, active, complete, ratio, created, finished, started int) string {
	vals := []string{
		rtString(name), rtInt(size), rtString(hash), rtString(label), rtString(dir),
		rtInt(active), rtInt(complete), rtInt(ratio),
		rtInt(created), rtInt(finished), rtInt(started),
	}
	var b strings.Builder
	b.WriteString("<array><data>")
	for _, v := range vals {
		b.WriteString("<value>" + v + "</value>")
	}
	b.WriteString("</data></array>")
	return b.String()
}

// rtMulticallResponse wraps rows in the outer array d.multicall2 returns.
func rtMulticallResponse(rows ...string) string {
	var b strings.Builder
	b.WriteString("<array><data>")
	for _, row := range rows {
		b.WriteString("<value>" + row + "</value>")
	}
	b.WriteString("</data></array>")
	return rtResp(b.String())
}

// rtServerState records the last method called and serves canned per-field
// responses for a single torrent fixture.
type rtServerState struct {
	mu          sync.Mutex
	lastMethod  string
	hash        string
	name        string
	size        int
	label       string
	complete    int // 0 or 1
	ratioMilli  int // ratio * 1000
	emptyLookup bool
}

func (s *rtServerState) record(m string) {
	s.mu.Lock()
	s.lastMethod = m
	s.mu.Unlock()
}

func (s *rtServerState) last() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastMethod
}

// newRTorrentServer mimics rtorrent's XML-RPC endpoint for the method subset
// the backend calls.
func newRTorrentServer(t *testing.T, st *rtServerState) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m := rtMethodRe.FindStringSubmatch(string(body))
		method := ""
		if len(m) == 2 {
			method = m[1]
		}
		st.record(method)
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, rtorrentRespFor(st, method))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// rtorrentRespFor returns the canned XML response for a given method.
func rtorrentRespFor(st *rtServerState, method string) string {
	switch method {
	case "d.multicall2":
		return rtMulticallResponse(rtMulticallRow(
			st.name, st.size, st.hash, st.label, "/downloads",
			1, st.complete, st.ratioMilli, 1700000000, 0, 1700000100,
		))
	case "d.name":
		if st.emptyLookup {
			return rtResp(rtString(""))
		}
		return rtResp(rtString(st.name))
	case "d.size_bytes", "d.completed_bytes":
		return rtResp(rtInt(st.size))
	case "d.custom1":
		return rtResp(rtString(st.label))
	case "d.directory":
		return rtResp(rtString("/downloads"))
	case "d.complete":
		return rtResp(rtInt(st.complete))
	case "d.ratio":
		return rtResp(rtInt(st.ratioMilli))
	case "d.down.rate", "d.up.rate", "throttle.global_down.rate", "throttle.global_up.rate":
		return rtResp(rtInt(2048))
	default: // load.start, load.raw_start, d.pause, d.resume, d.erase, d.hash
		return rtResp(rtInt(0))
	}
}

func newTestRTorrent(t *testing.T, addr string) *rtorrentBackend {
	t.Helper()
	c, err := newRTorrentClient(Options{
		Backend:  backendRTorrent,
		Timeout:  5 * time.Second,
		RTorrent: RTorrentOptions{Addr: addr},
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("newRTorrentClient: %v", err)
	}
	return c
}

func TestRTorrent_ListGetMutateStats(t *testing.T) {
	t.Parallel()
	const hash = "ABCDEF0123456789ABCDEF0123456789ABCDEF01"
	st := &rtServerState{
		hash: hash, name: "ubuntu.iso", size: 4096, label: "iso",
		complete: 1, ratioMilli: 1500,
	}
	srv := newRTorrentServer(t, st)
	c := newTestRTorrent(t, srv.URL)
	ctx := context.Background()

	if _, err := c.Add(ctx, "magnet:?xt=urn:btih:"+hash, AddOptions{Label: "iso", SavePath: "/x"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if st.last() != "load.start" {
		t.Errorf("Add called %q, want load.start", st.last())
	}

	if _, err := c.AddFile(ctx, []byte("data"), AddOptions{Paused: true}); err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	if st.last() != "load.raw" { // AddTorrentStopped maps to load.raw
		t.Errorf("AddFile(paused) called %q, want load.raw", st.last())
	}

	list, err := c.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List len = %d, want 1", len(list))
	}
	got := list[0]
	if got.Hash != strings.ToLower(hash) {
		t.Errorf("Hash = %q, want lowercase %q", got.Hash, strings.ToLower(hash))
	}
	if got.Name != "ubuntu.iso" || got.SizeBytes != 4096 {
		t.Errorf("List[0] = %+v", got)
	}
	if got.State != StateSeeding {
		t.Errorf("State = %q, want seeding", got.State)
	}
	if got.Ratio != 1.5 {
		t.Errorf("Ratio = %v, want 1.5", got.Ratio)
	}

	one, err := c.Get(ctx, strings.ToLower(hash))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if one.Hash != strings.ToLower(hash) {
		t.Errorf("Get hash = %q", one.Hash)
	}

	if err = c.Pause(ctx, hash); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if st.last() != "d.pause" {
		t.Errorf("Pause called %q, want d.pause", st.last())
	}
	if err = c.Resume(ctx, hash); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if st.last() != "d.resume" {
		t.Errorf("Resume called %q, want d.resume", st.last())
	}

	stats, err := c.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TorrentCount != 1 || stats.DownloadRate != 2048 || stats.UploadRate != 2048 {
		t.Errorf("Stats = %+v", stats)
	}

	if err = c.Remove(ctx, hash, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if st.last() != "d.erase" {
		t.Errorf("Remove called %q, want d.erase", st.last())
	}
}

func TestRTorrent_GetNotFound(t *testing.T) {
	t.Parallel()
	st := &rtServerState{emptyLookup: true}
	srv := newRTorrentServer(t, st)
	c := newTestRTorrent(t, srv.URL)

	_, err := c.Get(context.Background(), "deadbeef")
	if err == nil {
		t.Fatal("expected ErrNotFound for empty-hash lookup")
	}
}

func TestRTorrent_RemoveMissingIsNoError(t *testing.T) {
	t.Parallel()
	st := &rtServerState{emptyLookup: true}
	srv := newRTorrentServer(t, st)
	c := newTestRTorrent(t, srv.URL)

	if err := c.Remove(context.Background(), "deadbeef", false); err != nil {
		t.Fatalf("Remove(missing) = %v, want nil", err)
	}
}

func TestRTorrentState(t *testing.T) {
	t.Parallel()
	if got := rtorrentState(rt.Torrent{Completed: false}, true); got != StateSeeding {
		t.Errorf("completed flag => %q, want seeding", got)
	}
	if got := rtorrentState(rt.Torrent{Completed: false}, false); got != StateDownloading {
		t.Errorf("incomplete => %q, want downloading", got)
	}
	if got := rtorrentState(rt.Torrent{Completed: true}, false); got != StateSeeding {
		t.Errorf("torrent.Completed => %q, want seeding", got)
	}
}
