package pkgb

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

const (
	labelSession = "org.testcontainers.sessionId"
	labelRyuk    = "org.testcontainers.ryuk"
)

type stamped struct {
	at   time.Time
	line string
}

// TestStartAfterRyukExit waits for this session's Ryuk to exit on its
// reconnection timer, then starts a container DELAY_MS after the "die" event,
// i.e. inside the AutoRemove window when the delay is short.
func TestStartAfterRyukExit(t *testing.T) {
	ctx := context.Background()
	sid := testcontainers.SessionID()

	cli, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	defer cli.Close()

	evCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	ev := cli.Events(evCtx, client.EventsListOptions{Filters: make(client.Filters).
		Add("type", "container").
		Add("label", labelSession+"="+sid).
		Add("label", labelRyuk+"=true")})

	var (
		mu     sync.Mutex
		events []stamped
	)
	died := make(chan time.Time, 1)
	go func() {
		for m := range ev.Messages {
			at := time.Unix(0, m.TimeNano)
			mu.Lock()
			events = append(events, stamped{at, fmt.Sprintf("%s %s", m.Action, m.Actor.ID[:12])})
			mu.Unlock()
			if m.Action == "die" {
				select {
				case died <- at:
				default:
				}
			}
		}
	}()

	state := func() string {
		res, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: make(client.Filters).
			Add("label", labelSession+"="+sid).Add("label", labelRyuk+"=true")})
		if err != nil {
			return "list-error:" + err.Error()
		}
		if len(res.Items) == 0 {
			return "none"
		}
		parts := make([]string, 0, len(res.Items))
		for _, it := range res.Items {
			parts = append(parts, fmt.Sprintf("%s:%s", it.ID[:12], it.State))
		}
		return strings.Join(parts, ",")
	}

	initial := state()
	t.Logf("HARNESS pkgb session=%s ryuk-at-start=%s", sid[:12], initial)
	if !strings.Contains(initial, ":running") {
		t.Fatalf("HARNESS INCONCLUSIVE: no running Ryuk when pkgb started (%s)", initial)
	}

	if n, _ := strconv.Atoi(os.Getenv("PAD_FILES")); n > 0 {
		id := strings.SplitN(initial, ":", 2)[0]
		padStart := time.Now()
		if err := padRyuk(ctx, cli, id, n); err != nil {
			t.Fatalf("HARNESS INCONCLUSIVE: pad ryuk: %v", err)
		}
		t.Logf("HARNESS padded ryuk %s with %d files in %s", id, n, time.Since(padStart).Round(time.Millisecond))
	}

	var dieAt time.Time
	select {
	case dieAt = <-died:
	case <-time.After(60 * time.Second):
		t.Fatalf("HARNESS INCONCLUSIVE: Ryuk did not exit within 60s")
	}

	delay, _ := strconv.Atoi(os.Getenv("DELAY_MS"))
	time.Sleep(time.Duration(delay) * time.Millisecond)

	probeAt := time.Now()
	probe := state()
	t.Logf("HARNESS probe at die+%dms: %s", probeAt.Sub(dieAt).Milliseconds(), probe)

	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	c, err := testcontainers.Run(runCtx, "alpine:3.20", testcontainers.WithCmd("sleep", "300"))
	testcontainers.CleanupContainer(t, c)
	elapsed := time.Since(start)
	after := state()

	time.Sleep(300 * time.Millisecond)
	stopEvents()
	mu.Lock()
	for _, e := range events {
		t.Logf("HARNESS event die%+dms %s", e.at.Sub(dieAt).Milliseconds(), e.line)
	}
	mu.Unlock()

	if err != nil {
		t.Fatalf("HARNESS RESULT=FAIL probe=%q elapsed=%s ryuk-after=%s err=%v", probe, elapsed.Round(time.Millisecond), after, err)
	}
	t.Logf("HARNESS RESULT=PASS probe=%q elapsed=%s ryuk-after=%s", probe, elapsed.Round(time.Millisecond), after)
}
