// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package id provides standardized identifier generators for golusoris apps.
//
// Two flavors are available:
//   - [UUID]:  RFC 9562 UUIDv7 — time-ordered 128-bit, ideal for DB primary
//     keys (sorts naturally, plays well with btree indexes).
//   - [KSUID]: 27-char base62 — compact, sortable, good for public IDs.
//
// Apps should not call google/uuid or segmentio/ksuid directly; use this
// package so the convention is uniform.
package id

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/segmentio/ksuid"
	"go.uber.org/fx"
)

// Generator produces both flavors. NewUUID returns an error only when the
// system random source fails; callers surface it rather than panicking.
type Generator interface {
	NewUUID() (uuid.UUID, error)
	NewKSUID() ksuid.KSUID
}

type defaultGen struct{}

func (defaultGen) NewUUID() (uuid.UUID, error) {
	v, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("id: uuidv7: %w", err)
	}
	return v, nil
}

func (defaultGen) NewKSUID() ksuid.KSUID {
	return ksuid.New()
}

// New returns the default generator (UUIDv7 + KSUID).
func New() Generator { return defaultGen{} }

// Module provides the default generator via fx.
var Module = fx.Module(
	"golusoris.id",
	fx.Provide(New),
)
