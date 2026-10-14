// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ui_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/riverqueue/river"

	"github.com/golusoris/golusoris/jobs"
	"github.com/golusoris/golusoris/jobs/ui"
	rivertest "github.com/golusoris/golusoris/testutil/river"
)

type probeJobArgs struct {
	Payload string `json:"payload"`
}

func (probeJobArgs) Kind() string { return "probe-ui-job" }

func TestHandler_RealRiver(t *testing.T) {
	t.Parallel()

	rv := rivertest.Start(t, rivertest.Options{
		Register: func(w *jobs.Workers) {
			if err := jobs.Register(w, river.WorkFunc(func(context.Context, *river.Job[probeJobArgs]) error {
				return nil
			})); err != nil {
				t.Fatalf("jobs.Register: %v", err)
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	if _, err := rv.Client.Insert(ctx, probeJobArgs{Payload: "test-data"}, nil); err != nil {
		t.Fatalf("rv.Client.Insert: %v", err)
	}

	const prefix = "/jobs/ui"
	h, err := ui.NewHandler(ui.Options{
		Client: rv.Client,
		Prefix: prefix,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	stopUI, err := ui.Start(ctx, h)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(stopUI)

	authed := ui.WithBasicAuth(h, "admin", "secret")

	// 1. Wrong credentials -> 401
	reqWrong := httptest.NewRequest(http.MethodGet, prefix+"/api/jobs", nil)
	reqWrong.SetBasicAuth("admin", "wrong-password")
	recWrong := httptest.NewRecorder()
	authed.ServeHTTP(recWrong, reqWrong)
	if recWrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong creds status = %d, want 401", recWrong.Code)
	}

	// 2. Right credentials -> 200 and lists inserted job
	clk := clockwork.NewRealClock()
	deadline := clk.Now().Add(10 * time.Second)
	var bodyStr string
	found := false
	for clk.Now().Before(deadline) {
		for _, state := range []string{"completed", "running", "available"} {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, prefix+"/api/jobs?state="+state, nil)
			req.SetBasicAuth("admin", "secret")
			authed.ServeHTTP(rec, req)
			if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "probe-ui-job") {
				bodyStr = rec.Body.String()
				found = true
				break
			}
		}
		if found {
			break
		}
		clk.Sleep(50 * time.Millisecond)
	}

	if !found {
		t.Fatalf("job probe-ui-job not found in jobs API response: %s", bodyStr)
	}
}
