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
	"github.com/google/uuid"
	"github.com/segmentio/ksuid"
	"go.uber.org/fx"
)

// Generator produces both flavors.
type Generator interface {
	NewUUID() uuid.UUID
	NewKSUID() ksuid.KSUID
}

type defaultGen struct{}

func (defaultGen) NewUUID() uuid.UUID {
	v, err := uuid.NewV7()
	if err != nil {
		// uuid.NewV7 only fails when the random source fails, which on Linux
		// means catastrophic system state — panic is appropriate.
		panic("id: UUIDv7 generation failed: " + err.Error())
	}
	return v
}

func (defaultGen) NewKSUID() ksuid.KSUID {
	return ksuid.New()
}

// New returns the default generator (UUIDv7 + KSUID).
func New() Generator { return defaultGen{} }

// Module provides the default generator via fx.
var Module = fx.Module("golusoris.id",
	fx.Provide(New),
)
