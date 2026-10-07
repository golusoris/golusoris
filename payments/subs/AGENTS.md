<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# payments/subs

Provider-agnostic subscription-lifecycle state machine. processor
(Stripe / Paddle / Lemon Squeezy / self-hosted) drives money and
reports events back; `subs` holds authoritative record + valid
transitions.

## Surface

- `subs.New(Store, clock.Clock, *slog.Logger, Options)` → `*Service`.
- `Options{OnChange, IDGen, PeriodLength}`.
- `subs.Subscription{ID, CustomerID, Plan, Seats, Status, TrialEndsAt,
 CurrentPeriodStart, CurrentPeriodEnd, CancelAt, CanceledAt, PausedAt,
 Metadata, CreatedAt, UpdatedAt}`.
- `subs.Store` interface (Get, GetByCustomer, Upsert, Delete).
- `subs.NewMemoryStore()` for tests.

## States

```text
incomplete ──(Activate)─► active ◄──(Unpause)── paused
    │                       │    ──(Pause)────►
    ▼                       │
canceled             past_due
    ▲                       │
trialing ──(Activate)───────┘
```

Canceled is terminal. Activate rehydrates from incomplete/trialing/
past_due. Pause/Unpause round-trip between active and paused.

## Transitions

| Method | From | To | Notes |
|---|---|---|---|
| `Start(params)` | — | incomplete or trialing | `Trial>0` → trialing |
| `Activate(id)` | incomplete, trialing, past_due | active | Clears TrialEndsAt, starts fresh period |
| `Cancel(id, at)` | any non-canceled | canceled (now) or active (scheduled) | `at=zero` = immediate; future `at` = schedules `CancelAt` |
| `Resume(id)` | active with CancelAt set | active | Clears `CancelAt` |
| `Pause(id)` | active | paused | |
| `Unpause(id)` | paused | active | |
| `MarkPastDue(id)` | active | past_due | Call from failed-payment webhook |
| `Renew(id)` | active, trialing | active (next period) | No gap: new CurrentPeriodStart = old End. Trialing → active when past TrialEndsAt. |
| `ChangePlan(id, plan, seats)` | any | unchanged | Plan/Seats update only. Proration is the app's concern. |
| `ProcessDue([]id)` | — | — | Batch scanner for scheduled cancels + expired trials. |

## Events

`Options.OnChange` fires after successful transition with independent post-state snapshot. Keep fast or fan out; callback blocks transition.

## Persistence

`MemoryStore` for tests. Apps write Postgres-backed Store against
their own schema — interface is minimal (Get/GetByCustomer/Upsert/
Delete). When using Postgres, put INSERT/UPDATE inside same tx as
your app's row changes to keep subscription state consistent with your
domain.

## What this package does NOT do

- It does not charge cards. payment processor does.
- It does not compute proration amounts. `ChangePlan` updates  record; app computes charges via its payment processor's API.
- It does not store invoices. See `payments/invoice` for that.
- It does not emit webhooks. See `webhooks/out` for delivery.
