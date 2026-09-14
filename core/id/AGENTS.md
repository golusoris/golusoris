<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — id/

Standardized identifier generators so every app uses the same conventions
instead of calling google/uuid or segmentio/ksuid directly.

Two flavors:

- **UUIDv7** (RFC 9562) — time-ordered 128-bit. Ideal for DB primary keys:
  sorts naturally, plays well with btree indexes.
- **KSUID** — 27-char base62. Compact, sortable, good for public-facing IDs.

## Key API

| Symbol | Purpose |
|---|---|
| `id.Generator` | interface: `NewUUID() (uuid.UUID, error)`, `NewKSUID() ksuid.KSUID` |
| `id.New()` | the default generator |
| `id.Module` | fx module — provides `Generator` |

## Usage

```go
fx.New(golusoris.Core, id.Module)

func NewUserSvc(g id.Generator) *UserSvc { return &UserSvc{ids: g} }

func (s *UserSvc) Create() (User, error) {
    u, err := s.ids.NewUUID() // UUIDv7, time-ordered
    if err != nil {
        return User{}, err
    }
    return User{ID: u}, nil
}
```

## Don't

- Don't call `uuid.NewV7()` / `ksuid.New()` directly in app code — go through
  `Generator` so generation stays uniform and mockable.
- Don't use a KSUID where you want a sortable DB primary key — prefer UUIDv7.
- Note: `NewUUID` returns an error only when the system random source fails
  (catastrophic on Linux). Surface it; never discard it.
