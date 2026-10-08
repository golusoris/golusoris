// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package drain_test

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/drain"
)

var errInner = errors.New("inner stop")

// orderGate records that its wrapper ran before the hook's own OnStop.
type orderGate struct{ calls *[]string }

func (g orderGate) Wrap(hook fx.Hook) fx.Hook {
	stop := hook.OnStop
	hook.OnStop = func(ctx context.Context) error {
		*g.calls = append(*g.calls, "drain")
		return stop(ctx)
	}
	return hook
}

func TestWrapDefersStopBehindGate(t *testing.T) {
	t.Parallel()
	var calls []string
	hook := drain.Wrap(orderGate{calls: &calls}, fx.Hook{OnStop: func(context.Context) error {
		calls = append(calls, "stop")
		return errInner
	}})
	if err := hook.OnStop(t.Context()); !errors.Is(err, errInner) {
		t.Fatalf("OnStop error = %v, want %v", err, errInner)
	}
	if len(calls) != 2 || calls[0] != "drain" || calls[1] != "stop" {
		t.Fatalf("calls = %v, want [drain stop]", calls)
	}
}

func TestWrapWithoutGateKeepsHook(t *testing.T) {
	t.Parallel()
	stopped := false
	hook := drain.Wrap(nil, fx.Hook{OnStop: func(context.Context) error {
		stopped = true
		return nil
	}})
	if err := hook.OnStop(t.Context()); err != nil || !stopped {
		t.Fatalf("OnStop = %v, stopped = %v; want nil, true", err, stopped)
	}
}

func TestWrapWithoutGateKeepsEmptyHook(t *testing.T) {
	t.Parallel()
	hook := drain.Wrap(nil, fx.Hook{})
	if hook.OnStart != nil || hook.OnStop != nil {
		t.Fatal("an empty hook gained callbacks")
	}
}
