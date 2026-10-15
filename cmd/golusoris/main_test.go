// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/internal/testbin"
)

const pkg = "github.com/golusoris/golusoris/cmd/golusoris"

// releaseFlags mirrors the ldflags tools/.goreleaser.yml passes.
const releaseFlags = "-X main.version=v9.9.9 -X main.commit=abc1234 -X main.date=2026-10-13T00:00:00Z"

func TestBinaryReportsReleaseVersion(t *testing.T) {
	t.Parallel()
	res := testbin.Run(t, testbin.Build(t, pkg, releaseFlags), "--version")

	require.Zero(t, res.ExitCode, res.Stderr)
	require.Equal(t, "golusoris version v9.9.9 (commit abc1234, built 2026-10-13T00:00:00Z)\n", res.Stdout)
}

func TestBinaryWithoutLinkerFlagsReportsDev(t *testing.T) {
	t.Parallel()
	res := testbin.Run(t, testbin.Build(t, pkg, ""), "--version")

	require.Equal(t, "golusoris version dev (commit none, built unknown)\n", res.Stdout)
}

func TestBinaryWiresSubcommands(t *testing.T) {
	t.Parallel()
	bin := testbin.Build(t, pkg, "")

	help := testbin.Run(t, bin, "--help")
	require.Zero(t, help.ExitCode, help.Stderr)
	for _, sub := range []string{"init", "add", "bump"} {
		require.Regexp(t, `(?m)^\s+`+sub+`\s`, help.Stdout)
	}

	added := testbin.Run(t, bin, "add", "db")
	require.Zero(t, added.ExitCode, added.Stderr)
	require.Contains(t, added.Stdout, `Add "db" to your fx.New() call`)
	require.Contains(t, added.Stdout, "golusoris.DB,")
}

func TestBinaryRejectsUnknownCommand(t *testing.T) {
	t.Parallel()
	res := testbin.Run(t, testbin.Build(t, pkg, ""), "frobnicate")

	require.Equal(t, 1, res.ExitCode)
	require.Contains(t, res.Stderr, `unknown command "frobnicate"`)
}
