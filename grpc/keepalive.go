// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grpc

import (
	"errors"
	"fmt"
	"math"
	"time"

	"google.golang.org/grpc/keepalive"
)

// Infinite disables a keepalive duration. Any negative value works the same:
// a negative MaxConnectionAge never rotates connections, a negative
// MaxConnectionAgeGrace lets in-flight RPCs outlive a rotation, and a negative
// Time stops server pings. In config files use a negative duration ("-1s").
const Infinite time.Duration = -1

const (
	defaultMaxConnectionAge      = 2 * time.Minute
	defaultMaxConnectionAgeGrace = 5 * time.Second
	defaultKeepaliveTime         = time.Minute
	defaultKeepaliveTimeout      = 20 * time.Second
	// defaultKeepaliveMinTime matches grpc-go's own enforcement default.
	defaultKeepaliveMinTime = 5 * time.Minute
	// grpcInfinity is grpc-go's internal "never" for keepalive timers.
	grpcInfinity = time.Duration(math.MaxInt64)
)

var errNegativeKeepalive = errors.New("grpc: keepalive timeout and min_time must not be negative")

// KeepaliveConfig tunes server keepalive, connection rotation, and the ping
// policy enforced on clients (grpc.keepalive.*). Zero fields take the defaults.
type KeepaliveConfig struct {
	// MaxConnectionAge sends GOAWAY so clients move new RPCs to a fresh
	// connection (default 2m; negative = never).
	MaxConnectionAge time.Duration `koanf:"max_connection_age"`
	// MaxConnectionAgeGrace is how long RPCs still running at rotation may
	// continue before the connection closes (default 5s; negative = unlimited).
	MaxConnectionAgeGrace time.Duration `koanf:"max_connection_age_grace"`
	// Time is the idle period after which the server pings the client
	// (default 1m; negative = never).
	Time time.Duration `koanf:"time"`
	// Timeout closes the connection when a ping stays unacknowledged
	// (default 20s).
	Timeout time.Duration `koanf:"timeout"`
	// MinTime is the shortest client ping interval the server tolerates;
	// faster clients get GOAWAY (default 5m).
	MinTime time.Duration `koanf:"min_time"`
	// PermitWithoutStream allows client pings while no RPC is active.
	PermitWithoutStream bool `koanf:"permit_without_stream"`
}

func defaultKeepalive() KeepaliveConfig {
	return KeepaliveConfig{
		MaxConnectionAge:      defaultMaxConnectionAge,
		MaxConnectionAgeGrace: defaultMaxConnectionAgeGrace,
		Time:                  defaultKeepaliveTime,
		Timeout:               defaultKeepaliveTimeout,
		MinTime:               defaultKeepaliveMinTime,
	}
}

func (k KeepaliveConfig) withDefaults() KeepaliveConfig {
	d := defaultKeepalive()
	k.MaxConnectionAge = durationOr(k.MaxConnectionAge, d.MaxConnectionAge)
	k.MaxConnectionAgeGrace = durationOr(k.MaxConnectionAgeGrace, d.MaxConnectionAgeGrace)
	k.Time = durationOr(k.Time, d.Time)
	k.Timeout = durationOr(k.Timeout, d.Timeout)
	k.MinTime = durationOr(k.MinTime, d.MinTime)
	return k
}

func (k KeepaliveConfig) validate() error {
	if k.Timeout < 0 || k.MinTime < 0 {
		return fmt.Errorf("%w: timeout=%s min_time=%s", errNegativeKeepalive, k.Timeout, k.MinTime)
	}
	return nil
}

func (k KeepaliveConfig) serverParameters() keepalive.ServerParameters {
	return keepalive.ServerParameters{
		MaxConnectionAge:      infiniteIfNegative(k.MaxConnectionAge),
		MaxConnectionAgeGrace: infiniteIfNegative(k.MaxConnectionAgeGrace),
		Time:                  infiniteIfNegative(k.Time),
		Timeout:               k.Timeout,
	}
}

func (k KeepaliveConfig) enforcementPolicy() keepalive.EnforcementPolicy {
	return keepalive.EnforcementPolicy{MinTime: k.MinTime, PermitWithoutStream: k.PermitWithoutStream}
}

func durationOr(v, fallback time.Duration) time.Duration {
	if v == 0 {
		return fallback
	}
	return v
}

func infiniteIfNegative(d time.Duration) time.Duration {
	if d < 0 {
		return grpcInfinity
	}
	return d
}
