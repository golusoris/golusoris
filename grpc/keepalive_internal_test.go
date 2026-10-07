// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grpc

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/core/config"
)

func TestKeepalive_defaultsKeepTodaysValues(t *testing.T) {
	t.Parallel()
	got := KeepaliveConfig{}.withDefaults()
	require.Equal(t, 2*time.Minute, got.MaxConnectionAge)
	require.Equal(t, 5*time.Second, got.MaxConnectionAgeGrace)
	require.Equal(t, time.Minute, got.Time)
	require.Equal(t, 20*time.Second, got.Timeout)
	require.Equal(t, 5*time.Minute, got.MinTime)
	require.False(t, got.PermitWithoutStream)
	require.Equal(t, DefaultConfig().Keepalive, got)
	require.Equal(t, got, Config{}.withDefaults().Keepalive)
}

func TestKeepalive_preservesExplicitAndInfinite(t *testing.T) {
	t.Parallel()
	in := KeepaliveConfig{
		MaxConnectionAge:      Infinite,
		MaxConnectionAgeGrace: -time.Second,
		Time:                  time.Nanosecond,
		Timeout:               time.Second,
		MinTime:               10 * time.Second,
		PermitWithoutStream:   true,
	}
	require.Equal(t, in, in.withDefaults())
}

func TestKeepalive_serverParameters(t *testing.T) {
	t.Parallel()
	got := KeepaliveConfig{
		MaxConnectionAge:      Infinite,
		MaxConnectionAgeGrace: -time.Nanosecond, // boundary: smallest negative
		Time:                  Infinite,
		Timeout:               3 * time.Second,
	}.serverParameters()
	require.Equal(t, grpcInfinity, got.MaxConnectionAge)
	require.Equal(t, grpcInfinity, got.MaxConnectionAgeGrace)
	require.Equal(t, grpcInfinity, got.Time)
	require.Equal(t, 3*time.Second, got.Timeout)

	finite := defaultKeepalive().serverParameters()
	require.Equal(t, 2*time.Minute, finite.MaxConnectionAge)
	require.Equal(t, 5*time.Second, finite.MaxConnectionAgeGrace)

	policy := KeepaliveConfig{MinTime: time.Minute, PermitWithoutStream: true}.enforcementPolicy()
	require.Equal(t, time.Minute, policy.MinTime)
	require.True(t, policy.PermitWithoutStream)
}

func TestKeepalive_validate(t *testing.T) {
	t.Parallel()
	require.NoError(t, defaultKeepalive().validate())
	require.NoError(t, KeepaliveConfig{}.validate(), "zero is valid before defaults")
	require.ErrorIs(t, KeepaliveConfig{Timeout: -time.Nanosecond}.validate(), errNegativeKeepalive)
	require.ErrorIs(t, KeepaliveConfig{MinTime: Infinite}.validate(), errNegativeKeepalive)

	_, err := frameworkServerOptions(Config{Keepalive: KeepaliveConfig{Timeout: -1}}.withDefaults(), slog.New(slog.DiscardHandler), nil)
	require.ErrorIs(t, err, errNegativeKeepalive)
}

// TestLoadConfig_keepaliveFromEnv proves #589's override path: with
// CompoundKeys, APP_GRPC_KEEPALIVE_* reaches the snake_case keys.
func TestLoadConfig_keepaliveFromEnv(t *testing.T) {
	t.Setenv("TESTKA_GRPC_KEEPALIVE_MAX_CONNECTION_AGE", "-1s")
	t.Setenv("TESTKA_GRPC_KEEPALIVE_MAX_CONNECTION_AGE_GRACE", "10m")
	t.Setenv("TESTKA_GRPC_KEEPALIVE_TIME", "30s")
	cfg, err := config.New(config.Options{EnvPrefix: "TESTKA_", CompoundKeys: CompoundKeys()})
	require.NoError(t, err)
	c, err := loadConfig(cfg)
	require.NoError(t, err)
	require.Equal(t, -time.Second, c.Keepalive.MaxConnectionAge)
	require.Equal(t, 10*time.Minute, c.Keepalive.MaxConnectionAgeGrace)
	require.Equal(t, 30*time.Second, c.Keepalive.Time)
	require.Equal(t, 20*time.Second, c.Keepalive.Timeout, "unset keys keep defaults")
}

// TestLoadConfig_compoundKeysNeeded documents the env split without CompoundKeys.
func TestLoadConfig_compoundKeysNeeded(t *testing.T) {
	t.Setenv("TESTKB_GRPC_KEEPALIVE_MAX_CONNECTION_AGE", "-1s")
	cfg, err := config.New(config.Options{EnvPrefix: "TESTKB_"})
	require.NoError(t, err)
	c, err := loadConfig(cfg)
	require.NoError(t, err)
	require.Equal(t, defaultMaxConnectionAge, c.Keepalive.MaxConnectionAge)
}
