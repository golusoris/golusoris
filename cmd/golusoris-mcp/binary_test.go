// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main_test

import (
	"context"
	"os/exec"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/internal/testbin"
)

const pkg = "github.com/golusoris/golusoris/cmd/golusoris-mcp"

func TestBinaryReportsReleaseVersion(t *testing.T) {
	t.Parallel()
	res := testbin.Run(t, testbin.Build(t, pkg, "-X main.version=v9.9.9"), "--version")

	require.Zero(t, res.ExitCode, res.Stderr)
	require.Equal(t, "golusoris-mcp version v9.9.9\n", res.Stdout)
}

// TestBinaryServesStdio starts the built binary the way an IDE client does and
// checks the server reports the linker-set version over the protocol.
func TestBinaryServesStdio(t *testing.T) {
	t.Parallel()
	bin := testbin.Build(t, pkg, "-X main.version=v9.9.9")
	ctx, cancel := context.WithTimeout(t.Context(), testbin.Timeout)
	defer cancel()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, &sdkmcp.CommandTransport{Command: exec.CommandContext(ctx, bin)}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	info := cs.InitializeResult().ServerInfo
	require.Equal(t, "golusoris-mcp", info.Name)
	require.Equal(t, "v9.9.9", info.Version)

	tools, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 3)

	res, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "golusoris_add",
		Arguments: map[string]any{"module": "db"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	text, ok := res.Content[0].(*sdkmcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "Run:\n  golusoris add db", text.Text)
}

func TestBinaryWithoutLinkerFlagsReportsDev(t *testing.T) {
	t.Parallel()
	res := testbin.Run(t, testbin.Build(t, pkg, ""), "--version")

	require.Equal(t, "golusoris-mcp version dev\n", res.Stdout)
}

func TestBinaryRejectsUnknownTransport(t *testing.T) {
	t.Parallel()
	res := testbin.Run(t, testbin.Build(t, pkg, ""), "--transport", "carrier-pigeon")

	require.Equal(t, 1, res.ExitCode)
	require.Contains(t, res.Stderr, `unknown transport "carrier-pigeon"`)
}
