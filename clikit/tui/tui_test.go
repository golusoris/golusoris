// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tui_test

import (
	"context"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/clikit/tui"
)

type quitModel struct{}

func (quitModel) Init() tea.Cmd                         { return tea.Quit }
func (m quitModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (quitModel) View() tea.View                        { return tea.NewView("") }

func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("positive clean exit", func(t *testing.T) {
		t.Parallel()
		err := tui.Run(
			quitModel{},
			tea.WithoutRenderer(),
			tea.WithInput(strings.NewReader("")),
			tea.WithOutput(io.Discard),
			tea.WithoutSignals(),
		)
		require.NoError(t, err)
	})

	t.Run("negative cancelled context", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := tui.Run(
			quitModel{},
			tea.WithContext(ctx),
			tea.WithoutRenderer(),
			tea.WithInput(strings.NewReader("")),
			tea.WithOutput(io.Discard),
			tea.WithoutSignals(),
		)
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestRunInline(t *testing.T) {
	t.Parallel()

	t.Run("positive clean exit", func(t *testing.T) {
		t.Parallel()
		err := tui.RunInline(
			quitModel{},
			tea.WithoutRenderer(),
			tea.WithInput(strings.NewReader("")),
			tea.WithOutput(io.Discard),
			tea.WithoutSignals(),
		)
		require.NoError(t, err)
	})

	t.Run("negative cancelled context", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := tui.RunInline(
			quitModel{},
			tea.WithContext(ctx),
			tea.WithoutRenderer(),
			tea.WithInput(strings.NewReader("")),
			tea.WithOutput(io.Discard),
			tea.WithoutSignals(),
		)
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestQuit(t *testing.T) {
	t.Parallel()

	cmd := tui.Quit()
	require.NotNil(t, cmd)
	msg := cmd()
	require.Equal(t, tea.QuitMsg{}, msg)
}
