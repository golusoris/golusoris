// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nats

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/internal/tlsfiles"
	"github.com/golusoris/golusoris/internal/tlsfiles/tlsfilestest"
	"github.com/golusoris/golusoris/pubsub/cloudevents"
)

// applied runs opts against fresh nats.Options so tests can inspect the result.
func applied(t *testing.T, opts []nats.Option) nats.Options {
	t.Helper()
	var o nats.Options
	for _, opt := range opts {
		require.NoError(t, opt(&o))
	}
	return o
}

func writeUserSeed(t *testing.T) string {
	t.Helper()
	kp, err := nkeys.CreateUser()
	require.NoError(t, err)
	seed, err := kp.Seed()
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "user.nk")
	require.NoError(t, os.WriteFile(path, seed, 0o600))
	return path
}

// writeUserCreds writes a decorated creds file; nats.go parses but does not verify the JWT.
func writeUserCreds(t *testing.T) string {
	t.Helper()
	kp, err := nkeys.CreateUser()
	require.NoError(t, err)
	seed, err := kp.Seed()
	require.NoError(t, err)
	creds := "-----BEGIN NATS USER JWT-----\n" +
		"eyJ0eXAiOiJKV1QiLCJhbGciOiJlZDI1NTE5LW5rZXkifQ.e30.c2ln\n" +
		"------END NATS USER JWT------\n\n" +
		"-----BEGIN USER NKEY SEED-----\n" + string(seed) + "\n------END USER NKEY SEED------\n"
	path := filepath.Join(t.TempDir(), "user.creds")
	require.NoError(t, os.WriteFile(path, []byte(creds), 0o600))
	return path
}

func TestConnectOptionsPlain(t *testing.T) {
	t.Parallel()
	opts, err := connectOptions(Config{Name: "svc"}, nil, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	o := applied(t, opts)
	require.Equal(t, "svc", o.Name)
	require.False(t, o.Secure)
	require.Nil(t, o.UserJWT)
	require.Empty(t, o.Nkey)
}

func TestConnectOptionsAuth(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)

	opts, err := connectOptions(Config{Creds: writeUserCreds(t)}, nil, logger)
	require.NoError(t, err)
	require.NotNil(t, applied(t, opts).UserJWT)

	opts, err = connectOptions(Config{Creds: filepath.Join(t.TempDir(), "missing.creds")}, nil, logger)
	require.NoError(t, err, "nats.go opens the creds file when the connection applies its options")
	var o nats.Options
	require.ErrorIs(t, opts[len(opts)-1](&o), os.ErrNotExist)

	opts, err = connectOptions(Config{NKey: writeUserSeed(t)}, nil, logger)
	require.NoError(t, err)
	require.NotEmpty(t, applied(t, opts).Nkey)

	_, err = connectOptions(Config{Creds: "a.creds", NKey: "a.nk"}, nil, logger)
	require.ErrorIs(t, err, ErrConflictingAuth)

	_, err = connectOptions(Config{NKey: filepath.Join(t.TempDir(), "missing.nk")}, nil, logger)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestConnectOptionsTLS(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	paths := tlsfilestest.Write(t)

	opts, err := connectOptions(Config{TLS: TLSConfig{CA: paths.CA, Cert: paths.Cert, Key: paths.Key}}, nil, logger)
	require.NoError(t, err)
	o := applied(t, opts)
	require.True(t, o.Secure)
	require.NotNil(t, o.TLSConfig.RootCAs)
	require.Len(t, o.TLSConfig.Certificates, 1)

	injected := &tls.Config{MinVersion: tls.VersionTLS13, ServerName: "nats.internal"}
	opts, err = connectOptions(Config{}, injected, logger)
	require.NoError(t, err)
	o = applied(t, opts)
	require.True(t, o.Secure)
	require.Equal(t, "nats.internal", o.TLSConfig.ServerName)
	require.NotSame(t, injected, o.TLSConfig, "injected config is cloned")

	_, err = connectOptions(Config{TLS: TLSConfig{CA: paths.CA}}, injected, logger)
	require.ErrorIs(t, err, ErrConflictingTLS)

	_, err = connectOptions(Config{TLS: TLSConfig{Cert: paths.Cert}}, nil, logger)
	require.ErrorIs(t, err, tlsfiles.ErrPartialPair)
}

func TestPublishCloudEventValidatesBeforeJetStream(t *testing.T) {
	t.Parallel()
	client := &Client{}
	ctx := context.Background()
	valid := cloudevents.Event{ID: "1", Source: "s", Type: "t"}

	_, err := client.PublishCloudEvent(ctx, "events.test", cloudevents.Event{ID: "1", Source: "s"}, cloudevents.ModeBinary)
	var attrErr *cloudevents.AttributeError
	require.ErrorAs(t, err, &attrErr)
	require.Equal(t, "type", attrErr.Name)

	_, err = client.PublishCloudEvent(ctx, "events.test", valid, cloudevents.Mode(9))
	require.ErrorIs(t, err, cloudevents.ErrInvalidMode)

	_, err = client.PublishCloudEvent(ctx, "events.test", valid, cloudevents.ModeBinary)
	require.ErrorIs(t, err, ErrNoJetStream)
}

func TestPublishAckTimeoutDefault(t *testing.T) {
	t.Parallel()
	require.Equal(t, DefaultAckTimeout, (&Client{}).publishAckTimeout())
	require.Equal(t, DefaultAckTimeout, (&Client{ackTimeout: -1}).publishAckTimeout())
	require.Equal(t, 3*DefaultAckTimeout, (&Client{ackTimeout: 3 * DefaultAckTimeout}).publishAckTimeout())
	require.False(t, errors.Is(ErrNoJetStream, ErrConflictingTLS))
}
