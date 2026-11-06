//go:build sizing

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/core/clock"
)

// maxKeptErrors bounds the request errors kept for the failure message.
const maxKeptErrors = 5

var (
	errStatus    = errors.New("unexpected status")
	errSaturated = errors.New("sizing: all load workers busy; the app cannot keep the fixed rate")
	errAppDied   = errors.New("sizing: app exited during the load run")
)

type loadResult struct {
	sent      int64
	completed int64
	failed    int64
	firstErrs []error
	wall      time.Duration
	aborted   error
}

func (r loadResult) errSummary() string {
	return fmt.Sprintf("%d/%d requests failed, first %d errors: %v", r.failed, r.sent, len(r.firstErrs), errors.Join(r.firstErrs...))
}

// errSink counts every failure and keeps the first maxKeptErrors of them.
type errSink struct {
	mu    sync.Mutex
	n     int64
	first []error
}

func newErrSink() *errSink { return &errSink{first: make([]error, 0, maxKeptErrors)} }

func (s *errSink) add(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	if len(s.first) < maxKeptErrors {
		s.first = append(s.first, err)
	}
}

func (s *errSink) snapshot() (int64, []error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n, append([]error(nil), s.first...)
}

// getOK issues one GET bounded by requestTimeout and requires a 200.
func getOK(parent context.Context, client *http.Client, url string) error {
	ctx, cancel := context.WithTimeout(parent, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("sizing: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sizing: GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return fmt.Errorf("sizing: drain body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sizing: GET %s: %w %d", url, errStatus, resp.StatusCode)
	}
	return nil
}

// runLoad fires cfg.requests GETs at cfg.rps through loadWorkers workers and times the whole run; it stops early when exited closes.
func runLoad(t *testing.T, clk clock.Clock, url string, cfg sizingConfig, exited <-chan struct{}) loadResult {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: loadWorkers}}
	defer client.CloseIdleConnections()
	sink := newErrSink()
	var completed atomic.Int64
	jobs := make(chan struct{}, loadWorkers)
	var wg sync.WaitGroup
	for range loadWorkers {
		wg.Go(func() {
			for range jobs {
				if err := getOK(ctx, client, url); err != nil {
					sink.add(err)
				}
				completed.Add(1)
			}
		})
	}

	start := clk.Now()
	sent, aborted := dispatch(ctx, clk, jobs, sink, cfg, exited)
	if aborted != nil {
		cancel()
	}
	close(jobs)
	wg.Wait()
	wall := clk.Since(start)

	failed, first := sink.snapshot()
	return loadResult{sent: sent, completed: completed.Load(), failed: failed, firstErrs: first, wall: wall, aborted: aborted}
}

// dispatch hands one job per tick to the workers until cfg.requests are sent, the app exits or ctx ends.
func dispatch(ctx context.Context, clk clock.Clock, jobs chan<- struct{}, sink *errSink, cfg sizingConfig, exited <-chan struct{}) (int64, error) {
	ticker := clk.NewTicker(max(time.Second/time.Duration(cfg.rps), time.Nanosecond))
	defer ticker.Stop()
	var sent int64
	for sent < cfg.requests {
		select {
		case <-ticker.Chan():
		case <-exited:
			return sent, errAppDied
		case <-ctx.Done():
			return sent, fmt.Errorf("sizing: load run: %w", ctx.Err())
		}
		select {
		case jobs <- struct{}{}:
		default:
			sink.add(errSaturated)
		}
		sent++
	}
	return sent, nil
}
