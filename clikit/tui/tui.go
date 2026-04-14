// Package tui provides lightweight helpers for building terminal UIs with
// charmbracelet/bubbletea. It exports a thin Run wrapper and a few common
// model building blocks (Spinner, Confirm, Select) so callers can compose a
// full TUI without boilerplate.
//
// Usage:
//
//	type model struct{ done bool; result string }
//	func (m model) Init() tea.Cmd { return nil }
//	func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
//	    if _, ok := msg.(tea.KeyMsg); ok { m.done = true; return m, tea.Quit }
//	    return m, nil
//	}
//	func (m model) View() string { return "Press any key to quit\n" }
//
//	if err := tui.Run(model{}); err != nil {
//	    log.Fatal(err)
//	}
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Run starts the bubbletea program with the given initial model and blocks
// until it exits. It is a thin wrapper around tea.NewProgram that
// applies sensible defaults (AltScreen, mouse support).
func Run(m tea.Model, opts ...tea.ProgramOption) error {
	defaults := make([]tea.ProgramOption, 0, 2+len(opts))
	defaults = append(defaults, tea.WithAltScreen(), tea.WithMouseCellMotion())
	p := tea.NewProgram(m, append(defaults, opts...)...)
	_, err := p.Run()
	return err //nolint:wrapcheck // tea error is already descriptive
}

// RunInline runs the program without the alternate screen (inline mode).
// Useful for short-lived prompts that should scroll naturally.
func RunInline(m tea.Model, opts ...tea.ProgramOption) error {
	p := tea.NewProgram(m, opts...)
	_, err := p.Run()
	return err //nolint:wrapcheck // tea error is already descriptive
}

// Quit returns a tea.Cmd that immediately sends a tea.QuitMsg.
// Useful as a shorthand in model Update methods.
func Quit() tea.Cmd { return tea.Quit }
