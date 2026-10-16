// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package riverclientstate reads the River client settings that decide
// whether PeriodicJobs and Subscribe panic, so callers can check them
// instead of recovering the panic. A recover there also catches runtime
// faults, which corrupt the heap on Windows hosts with large exception
// contexts (golang/go#81238).
package riverclientstate

import (
	"reflect"

	"github.com/riverqueue/river"
)

// WorksJobs reports whether c has queues configured. It returns false for
// nil and fails closed when River no longer stores its configuration in the
// inspected field.
func WorksJobs[TTx any](c *river.Client[TTx]) bool {
	config, ok := clientConfig(c)
	return ok && config.FieldByName("Queues").Len() > 0
}

// LeaderElectionDisabled reports whether c was built with
// LeaderElectionDisabled. It returns true for nil and for an unknown River
// layout, the setting under which PeriodicJobs panics.
func LeaderElectionDisabled[TTx any](c *river.Client[TTx]) bool {
	config, ok := clientConfig(c)
	return !ok || config.FieldByName("LeaderElectionDisabled").Bool()
}

func clientConfig[TTx any](c *river.Client[TTx]) (reflect.Value, bool) {
	if c == nil {
		return reflect.Value{}, false
	}
	field := reflect.ValueOf(c).Elem().FieldByName("config")
	if !field.IsValid() || field.Kind() != reflect.Pointer || field.IsNil() {
		return reflect.Value{}, false
	}
	config := field.Elem()
	if config.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	queues := config.FieldByName("Queues")
	disabled := config.FieldByName("LeaderElectionDisabled")
	if queues.Kind() != reflect.Map || disabled.Kind() != reflect.Bool {
		return reflect.Value{}, false
	}
	return config, true
}
