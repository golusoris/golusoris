// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package passkeys

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewClonesAllowedOrigins(t *testing.T) {
	t.Parallel()
	origins := []string{"https://example.com"}
	service, err := New(Options{
		RPID:      "example.com",
		RPName:    "Example",
		RPOrigins: origins,
	})
	require.NoError(t, err)

	origins[0] = "https://attacker.example"
	require.Equal(t, []string{"https://example.com"}, service.wa.Config.RPOrigins)
}
