// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package audit

import (
	"testing"
	"time"
)

func TestMatchesFilter_zeroFilterMatchesAny(t *testing.T) {
	t.Parallel()
	e := Event{Actor: "user:1", Action: "login", Target: "session", TenantID: "acme"}
	if !matchesFilter(e, Filter{}) {
		t.Fatal("zero-value filter should match any event")
	}
}

func TestMatchesFilter_actorMismatch(t *testing.T) {
	t.Parallel()
	e := Event{Actor: "user:1"}
	if matchesFilter(e, Filter{Actor: "user:2"}) {
		t.Fatal("mismatched Actor should not match")
	}
}

func TestMatchesFilter_actionMismatch(t *testing.T) {
	t.Parallel()
	e := Event{Action: "login"}
	if matchesFilter(e, Filter{Action: "logout"}) {
		t.Fatal("mismatched Action should not match")
	}
}

func TestMatchesFilter_targetMismatch(t *testing.T) {
	t.Parallel()
	e := Event{Target: "order:1"}
	if matchesFilter(e, Filter{Target: "order:2"}) {
		t.Fatal("mismatched Target should not match")
	}
}

func TestMatchesFilter_tenantMismatch(t *testing.T) {
	t.Parallel()
	e := Event{TenantID: "acme"}
	if matchesFilter(e, Filter{TenantID: "other"}) {
		t.Fatal("mismatched TenantID should not match")
	}
}

func TestMatchesFilter_afterBoundary(t *testing.T) {
	t.Parallel()
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Equal to After is not strictly after: excluded.
	if matchesFilter(Event{CreatedAt: ref}, Filter{After: ref}) {
		t.Fatal("CreatedAt == After should not match (After is exclusive)")
	}
	// Strictly after: included.
	if !matchesFilter(Event{CreatedAt: ref.Add(time.Second)}, Filter{After: ref}) {
		t.Fatal("CreatedAt after After should match")
	}
}

func TestMatchesFilter_beforeBoundary(t *testing.T) {
	t.Parallel()
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Equal to Before is not strictly before: excluded.
	if matchesFilter(Event{CreatedAt: ref}, Filter{Before: ref}) {
		t.Fatal("CreatedAt == Before should not match (Before is exclusive)")
	}
	// Strictly before: included.
	if !matchesFilter(Event{CreatedAt: ref.Add(-time.Second)}, Filter{Before: ref}) {
		t.Fatal("CreatedAt before Before should match")
	}
}

func TestMatchesIdentity_directCall(t *testing.T) {
	t.Parallel()
	e := Event{Actor: "user:1", Action: "login", Target: "session", TenantID: "acme"}
	if !matchesIdentity(e, Filter{}) {
		t.Error("zero-value filter should match any identity")
	}
	if !matchesIdentity(e, Filter{Actor: "user:1", TenantID: "acme"}) {
		t.Error("matching Actor+TenantID should match")
	}
	if matchesIdentity(e, Filter{Actor: "user:2"}) {
		t.Error("mismatched Actor should not match")
	}
}

func TestMatchesTimeRange_directCall(t *testing.T) {
	t.Parallel()
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !matchesTimeRange(Event{CreatedAt: ref}, Filter{}) {
		t.Error("zero-value filter should match any time")
	}
	if !matchesTimeRange(Event{CreatedAt: ref}, Filter{After: ref.Add(-time.Minute), Before: ref.Add(time.Minute)}) {
		t.Error("CreatedAt strictly within (After, Before) should match")
	}
	if matchesTimeRange(Event{CreatedAt: ref}, Filter{After: ref}) {
		t.Error("CreatedAt == After should not match (exclusive)")
	}
}

func TestMatchesFilter_allCriteriaCombined(t *testing.T) {
	t.Parallel()
	ref := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e := Event{
		Actor: "user:1", Action: "order.cancel", Target: "order:9",
		TenantID: "acme", CreatedAt: ref,
	}
	f := Filter{
		Actor: "user:1", Action: "order.cancel", Target: "order:9",
		TenantID: "acme", After: ref.Add(-time.Minute), Before: ref.Add(time.Minute),
	}
	if !matchesFilter(e, f) {
		t.Fatal("event satisfying every filter criterion should match")
	}
}
