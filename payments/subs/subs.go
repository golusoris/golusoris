// Package subs provides a provider-agnostic subscription-lifecycle
// state machine for SaaS billing. Apps layer it on top of any payment
// processor (Stripe, Paddle, Lemon Squeezy, self-hosted) — the state
// machine holds the authoritative record; the processor drives the
// payments and reports outcomes back via the framework's webhook
// receiver + the Service's transition methods.
//
// States:
//
//	incomplete ──(Activate)─► active ◄──(Unpause)── paused
//	    │                       │    ──(Pause)────►
//	    ▼                       │
//	canceled             past_due
//	    ▲                       │
//	trialing ──(Activate)───────┘
//
// Every transition is written through [Store] and (when configured)
// emitted via [Options.OnChange].
package subs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/id"
)

// Status is the subscription lifecycle state.
type Status string

// Lifecycle states.
const (
	// StatusIncomplete — created but payment has not yet succeeded.
	StatusIncomplete Status = "incomplete"
	// StatusTrialing — in a free trial; will transition to active (or
	// canceled) when TrialEndsAt is reached.
	StatusTrialing Status = "trialing"
	// StatusActive — paid and current.
	StatusActive Status = "active"
	// StatusPastDue — payment failed; grace period before cancellation.
	StatusPastDue Status = "past_due"
	// StatusPaused — seat/service paused by customer or admin.
	StatusPaused Status = "paused"
	// StatusCanceled — terminal state; may or may not be effective yet.
	StatusCanceled Status = "canceled"
)

// Subscription is the authoritative record.
type Subscription struct {
	ID                 string
	CustomerID         string
	Plan               string
	Seats              int
	Status             Status
	TrialEndsAt        *time.Time
	CurrentPeriodStart time.Time
	CurrentPeriodEnd   time.Time
	// CancelAt holds a future effective cancellation time.
	// Non-nil + Status=active means "scheduled to cancel at CancelAt".
	CancelAt *time.Time
	// CanceledAt is the actual cancellation timestamp (status=canceled).
	CanceledAt *time.Time
	// PausedAt is the pause timestamp (status=paused).
	PausedAt  *time.Time
	Metadata  map[string]string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store persists subscriptions.
type Store interface {
	Get(ctx context.Context, id string) (*Subscription, error)
	GetByCustomer(ctx context.Context, customerID string) ([]*Subscription, error)
	Upsert(ctx context.Context, s *Subscription) error
	Delete(ctx context.Context, id string) error
}

// ErrNotFound is returned by [Store.Get] when no subscription matches.
var ErrNotFound = errors.New("payments/subs: subscription not found")

// ChangeEvent describes a status transition.
type ChangeEvent struct {
	Subscription *Subscription
	From         Status
	To           Status
	At           time.Time
}

// Options configures [Service].
type Options struct {
	// OnChange is invoked after a successful transition (persist has
	// already happened). Keep the callback fast; fan out to a worker if
	// needed.
	OnChange func(context.Context, ChangeEvent)
	// IDGen overrides the default UUIDv7 generator.
	IDGen func() string
	// PeriodLength is the default billing period when apps don't set
	// one explicitly (Start/Renew use it). Default: 30 days.
	PeriodLength time.Duration
}

// Service runs the lifecycle transitions.
type Service struct {
	store  Store
	clock  clock.Clock
	logger *slog.Logger
	opts   Options
}

// New returns a Service. Logger may be nil (discards events).
func New(store Store, clk clock.Clock, logger *slog.Logger, opts Options) *Service {
	if opts.PeriodLength == 0 {
		opts.PeriodLength = 30 * 24 * time.Hour
	}
	if opts.IDGen == nil {
		g := id.New()
		opts.IDGen = func() string { return g.NewUUID().String() }
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Service{store: store, clock: clk, logger: logger, opts: opts}
}

// StartParams are inputs for [Service.Start].
type StartParams struct {
	CustomerID string
	Plan       string
	Seats      int
	// Trial, when >0, puts the subscription into "trialing" for the
	// given duration. Otherwise the subscription starts as
	// "incomplete" and must be activated when payment succeeds.
	Trial    time.Duration
	Metadata map[string]string
}

// Start creates a new subscription. See StartParams for trial handling.
func (s *Service) Start(ctx context.Context, p StartParams) (*Subscription, error) {
	if p.CustomerID == "" || p.Plan == "" {
		return nil, errors.New("payments/subs: CustomerID and Plan required")
	}
	now := s.clock.Now()
	sub := &Subscription{
		ID:                 s.opts.IDGen(),
		CustomerID:         p.CustomerID,
		Plan:               p.Plan,
		Seats:              p.Seats,
		Status:             StatusIncomplete,
		CurrentPeriodStart: now,
		CurrentPeriodEnd:   now.Add(s.opts.PeriodLength),
		Metadata:           p.Metadata,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if p.Trial > 0 {
		te := now.Add(p.Trial)
		sub.TrialEndsAt = &te
		sub.Status = StatusTrialing
	}
	if err := s.store.Upsert(ctx, sub); err != nil {
		return nil, fmt.Errorf("payments/subs: upsert: %w", err)
	}
	s.emit(ctx, sub, "", sub.Status, now)
	return sub, nil
}

// Activate transitions a subscription to Active. Valid from: Incomplete,
// Trialing, PastDue.
func (s *Service) Activate(ctx context.Context, subID string) error {
	return s.transition(ctx, subID, StatusActive, func(sub *Subscription) error {
		switch sub.Status {
		case StatusIncomplete, StatusTrialing, StatusPastDue:
			return nil
		case StatusActive, StatusPaused, StatusCanceled:
			return fmt.Errorf("payments/subs: cannot Activate from %s", sub.Status)
		default:
			return fmt.Errorf("payments/subs: cannot Activate from %s", sub.Status)
		}
	}, func(sub *Subscription, now time.Time) {
		// On activation, clear TrialEndsAt and start a fresh period.
		sub.TrialEndsAt = nil
		sub.CurrentPeriodStart = now
		sub.CurrentPeriodEnd = now.Add(s.opts.PeriodLength)
	})
}

// Cancel schedules or performs a cancellation. When at==zero, the
// cancellation is immediate (Status=Canceled, CanceledAt=now). When at
// is in the future, the subscription stays Active with CancelAt=at;
// an external renewal task must flip it to Canceled at that time.
func (s *Service) Cancel(ctx context.Context, subID string, at time.Time) error {
	sub, err := s.fetch(ctx, subID)
	if err != nil {
		return err
	}
	if sub.Status == StatusCanceled {
		return nil // idempotent
	}
	now := s.clock.Now()
	from := sub.Status
	if at.IsZero() || !at.After(now) {
		sub.Status = StatusCanceled
		sub.CanceledAt = &now
		sub.CancelAt = nil
	} else {
		sub.CancelAt = &at
	}
	sub.UpdatedAt = now
	if err := s.store.Upsert(ctx, sub); err != nil {
		return fmt.Errorf("payments/subs: upsert: %w", err)
	}
	s.emit(ctx, sub, from, sub.Status, now)
	return nil
}

// Resume undoes a scheduled cancellation. Only valid while Status is
// not yet canceled.
func (s *Service) Resume(ctx context.Context, subID string) error {
	sub, err := s.fetch(ctx, subID)
	if err != nil {
		return err
	}
	if sub.Status == StatusCanceled {
		return errors.New("payments/subs: cannot resume a terminated subscription")
	}
	if sub.CancelAt == nil {
		return nil
	}
	sub.CancelAt = nil
	sub.UpdatedAt = s.clock.Now()
	return s.store.Upsert(ctx, sub) //nolint:wrapcheck
}

// Pause transitions Active→Paused.
func (s *Service) Pause(ctx context.Context, subID string) error {
	return s.transition(ctx, subID, StatusPaused, func(sub *Subscription) error {
		if sub.Status != StatusActive {
			return fmt.Errorf("payments/subs: cannot Pause from %s", sub.Status)
		}
		return nil
	}, func(sub *Subscription, now time.Time) {
		sub.PausedAt = &now
	})
}

// Unpause transitions Paused→Active.
func (s *Service) Unpause(ctx context.Context, subID string) error {
	return s.transition(ctx, subID, StatusActive, func(sub *Subscription) error {
		if sub.Status != StatusPaused {
			return fmt.Errorf("payments/subs: cannot Unpause from %s", sub.Status)
		}
		return nil
	}, func(sub *Subscription, _ time.Time) {
		sub.PausedAt = nil
	})
}

// MarkPastDue transitions Active→PastDue (payment failed). Apps call
// this from a failed-payment webhook handler.
func (s *Service) MarkPastDue(ctx context.Context, subID string) error {
	return s.transition(ctx, subID, StatusPastDue, func(sub *Subscription) error {
		if sub.Status != StatusActive {
			return fmt.Errorf("payments/subs: cannot MarkPastDue from %s", sub.Status)
		}
		return nil
	}, nil)
}

// Renew advances the billing period. Usually called from a successful-
// invoice-payment webhook handler. The new period starts at the old
// CurrentPeriodEnd so there are no gaps.
func (s *Service) Renew(ctx context.Context, subID string) error {
	sub, err := s.fetch(ctx, subID)
	if err != nil {
		return err
	}
	if sub.Status != StatusActive && sub.Status != StatusTrialing {
		return fmt.Errorf("payments/subs: cannot Renew from %s", sub.Status)
	}
	now := s.clock.Now()
	sub.CurrentPeriodStart = sub.CurrentPeriodEnd
	sub.CurrentPeriodEnd = sub.CurrentPeriodStart.Add(s.opts.PeriodLength)
	if sub.Status == StatusTrialing && sub.TrialEndsAt != nil && !now.Before(*sub.TrialEndsAt) {
		sub.TrialEndsAt = nil
		sub.Status = StatusActive
	}
	sub.UpdatedAt = now
	return s.store.Upsert(ctx, sub) //nolint:wrapcheck
}

// ChangePlan updates the plan + seats. When prorate is true, returns a
// [Proration] describing the credit/charge the caller should apply via
// the payment processor. The state machine itself doesn't bill.
func (s *Service) ChangePlan(ctx context.Context, subID, newPlan string, newSeats int) error {
	sub, err := s.fetch(ctx, subID)
	if err != nil {
		return err
	}
	sub.Plan = newPlan
	if newSeats > 0 {
		sub.Seats = newSeats
	}
	sub.UpdatedAt = s.clock.Now()
	return s.store.Upsert(ctx, sub) //nolint:wrapcheck
}

// ProcessDue walks all subscriptions with a non-nil CancelAt in the past
// and transitions them to Canceled. Apps call this from a scheduled
// job. Subscriptions whose trials have ended and still have no
// successful payment also drop to StatusCanceled.
//
// This helper needs the Store to support iteration; if it doesn't,
// apps can call Cancel directly from their own scanners.
func (s *Service) ProcessDue(ctx context.Context, subIDs []string) error {
	now := s.clock.Now()
	for _, sid := range subIDs {
		sub, err := s.fetch(ctx, sid)
		if err != nil {
			continue
		}
		if sub.CancelAt != nil && !sub.CancelAt.After(now) && sub.Status != StatusCanceled {
			_ = s.Cancel(ctx, sid, time.Time{})
			continue
		}
		if sub.Status == StatusTrialing && sub.TrialEndsAt != nil && !sub.TrialEndsAt.After(now) {
			// Trial ended without activation → cancel.
			_ = s.Cancel(ctx, sid, time.Time{})
		}
	}
	return nil
}

// fetch is a thin Store.Get wrapper that adds a contextual error
// message — keeps wrapcheck happy in one place rather than at every
// call site.
func (s *Service) fetch(ctx context.Context, subID string) (*Subscription, error) {
	sub, err := s.store.Get(ctx, subID)
	if err != nil {
		return nil, fmt.Errorf("payments/subs: get %s: %w", subID, err)
	}
	return sub, nil
}

// transition is the common path for simple status swaps.
func (s *Service) transition(
	ctx context.Context, subID string, to Status,
	guard func(*Subscription) error,
	mutate func(*Subscription, time.Time),
) error {
	sub, err := s.fetch(ctx, subID)
	if err != nil {
		return err
	}
	if sub.Status == to {
		return nil // idempotent
	}
	if guard != nil {
		if err := guard(sub); err != nil {
			return err
		}
	}
	now := s.clock.Now()
	from := sub.Status
	sub.Status = to
	sub.UpdatedAt = now
	if mutate != nil {
		mutate(sub, now)
	}
	if err := s.store.Upsert(ctx, sub); err != nil {
		return fmt.Errorf("payments/subs: upsert: %w", err)
	}
	s.emit(ctx, sub, from, to, now)
	return nil
}

func (s *Service) emit(ctx context.Context, sub *Subscription, from, to Status, at time.Time) {
	s.logger.DebugContext(ctx, "subs: transition",
		slog.String("id", sub.ID),
		slog.String("from", string(from)),
		slog.String("to", string(to)),
	)
	if s.opts.OnChange != nil {
		s.opts.OnChange(ctx, ChangeEvent{Subscription: sub, From: from, To: to, At: at})
	}
}
