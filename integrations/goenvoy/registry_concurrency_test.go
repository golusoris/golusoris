// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package goenvoy_test

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/goenvoy/metadata/video/tmdb"

	"github.com/golusoris/golusoris/httpx/client"
	"github.com/golusoris/golusoris/integrations/goenvoy"
)

type tmdbLookupResult struct {
	client *tmdb.Client
	err    error
}

func TestRegistryConcurrentSameKeyBuildsOnce(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseBuilds := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseBuilds)
	var builds atomic.Int32
	r := goenvoy.NewRegistryForTest(
		goenvoy.Options{Services: map[string]goenvoy.ServiceOptions{
			"tmdb": {Provider: goenvoy.ProviderTMDb, AccessToken: "token"},
		}},
		discardLogger(),
		nil,
		nil,
		func(client.Options) *http.Client {
			builds.Add(1)
			entered <- struct{}{}
			<-release
			return &http.Client{Transport: http.DefaultTransport}
		},
	)

	results := make(chan tmdbLookupResult, 2)
	lookup := func() {
		c, err := r.TMDb("tmdb")
		results <- tmdbLookupResult{client: c, err: err}
	}
	go lookup()
	waitForEntry(t, entered, "first build")
	go lookup()

	secondEntered := false
	timer := time.NewTimer(500 * time.Millisecond)
	select {
	case <-entered:
		secondEntered = true
	case <-timer.C:
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	releaseBuilds()

	first := <-results
	second := <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("TMDb errors = (%v, %v)", first.err, second.err)
	}
	if secondEntered || builds.Load() != 1 {
		t.Fatalf("same-key concurrent lookups built %d clients; want 1", builds.Load())
	}
	if first.client != second.client {
		t.Fatal("same-key concurrent lookups returned different clients")
	}
}

func TestRegistryConcurrentDifferentKeysBuildInParallel(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseBuilds := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseBuilds)
	r := goenvoy.NewRegistryForTest(
		goenvoy.Options{Services: map[string]goenvoy.ServiceOptions{
			"first":  {Provider: goenvoy.ProviderTMDb, AccessToken: "token-1"},
			"second": {Provider: goenvoy.ProviderTMDb, AccessToken: "token-2"},
		}},
		discardLogger(),
		nil,
		nil,
		func(client.Options) *http.Client {
			entered <- struct{}{}
			<-release
			return &http.Client{Transport: http.DefaultTransport}
		},
	)

	results := make(chan tmdbLookupResult, 2)
	for _, name := range []string{"first", "second"} {
		go func() {
			c, err := r.TMDb(name)
			results <- tmdbLookupResult{client: c, err: err}
		}()
	}
	waitForEntry(t, entered, "first distinct-key build")
	waitForEntry(t, entered, "second distinct-key build")
	releaseBuilds()

	first := <-results
	second := <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("TMDb errors = (%v, %v)", first.err, second.err)
	}
}

func waitForEntry(t *testing.T, entered <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}
