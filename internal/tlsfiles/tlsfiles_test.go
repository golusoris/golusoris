// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tlsfiles_test

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/internal/tlsfiles"
	"github.com/golusoris/golusoris/internal/tlsfiles/tlsfilestest"
)

func TestClientConfigLoadsFiles(t *testing.T) {
	t.Parallel()
	paths := tlsfilestest.Write(t)

	full, err := tlsfiles.ClientConfig(tlsfiles.Files{CA: paths.CA, Cert: paths.Cert, Key: paths.Key})
	require.NoError(t, err)
	require.NotNil(t, full.RootCAs)
	require.Len(t, full.Certificates, 1)
	require.Equal(t, uint16(tls.VersionTLS12), full.MinVersion)

	caOnly, err := tlsfiles.ClientConfig(tlsfiles.Files{CA: paths.CA})
	require.NoError(t, err)
	require.NotNil(t, caOnly.RootCAs)
	require.Empty(t, caOnly.Certificates)

	pairOnly, err := tlsfiles.ClientConfig(tlsfiles.Files{Cert: paths.Cert, Key: paths.Key})
	require.NoError(t, err)
	require.Nil(t, pairOnly.RootCAs, "system roots stay in effect without a CA file")
	require.Len(t, pairOnly.Certificates, 1)
}

func TestClientConfigRejects(t *testing.T) {
	t.Parallel()
	paths := tlsfilestest.Write(t)
	notPEM := filepath.Join(t.TempDir(), "empty.pem")
	require.NoError(t, os.WriteFile(notPEM, []byte("no certificates here"), 0o600))

	_, err := tlsfiles.ClientConfig(tlsfiles.Files{Cert: paths.Cert})
	require.ErrorIs(t, err, tlsfiles.ErrPartialPair)
	_, err = tlsfiles.ClientConfig(tlsfiles.Files{Key: paths.Key})
	require.ErrorIs(t, err, tlsfiles.ErrPartialPair)
	_, err = tlsfiles.ClientConfig(tlsfiles.Files{CA: notPEM})
	require.ErrorIs(t, err, tlsfiles.ErrEmptyCA)
	_, err = tlsfiles.ClientConfig(tlsfiles.Files{CA: filepath.Join(t.TempDir(), "missing.pem")})
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = tlsfiles.ClientConfig(tlsfiles.Files{Cert: paths.Cert, Key: paths.CA})
	require.Error(t, err)
}

func TestFilesIsZero(t *testing.T) {
	t.Parallel()
	require.True(t, tlsfiles.Files{}.IsZero())
	require.False(t, tlsfiles.Files{CA: "ca.pem"}.IsZero())
}
