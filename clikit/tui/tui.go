// Package tui provides lightweight helpers for building terminal UIs with
// charmbracelet/bubbletea v2. It exports a thin Run wrapper and a few common
// model building blocks (Spinner, Confirm, Select) so callers can compose a
// full TUI without boilerplate.
//
// Note (bubbletea v2): the alternate screen and mouse tracking are no longer
// program options. A model enables them by setting fields on the [tea.View]
// returned from its View method (View.AltScreen, View.MouseMode), so the Run
// wrapper can no longer inject them on a caller's behalf.
//
// Usage:
//
//	type model struct{ done bool; result string }
//	func (m model) Init() tea.Cmd { return nil }
//	func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
//	    if _, ok := msg.(tea.KeyMsg); ok { m.done = true; return m, tea.Quit }
//	    return m, nil
//	}
//	func (m model) View() tea.View {
//	    v := tea.NewView("Press any key to quit\n")
//	    v.AltScreen = true // opt into full-screen mode here, not via Run
//	    return v
//	}
//
//	if err := tui.Run(model{}); err != nil {
//	    log.Fatal(err)
//	}
package tui

import (
	tea "charm.land/bubbletea/v2"
)

// Run starts the bubbletea program with the given initial model and blocks
// until it exits. It is a thin wrapper around tea.NewProgram.
//
// In bubbletea v2 the alternate screen and mouse tracking are controlled by
// the model via the [tea.View] it returns (View.AltScreen, View.MouseMode),
// not by program options, so Run no longer enables them automatically.
func Run(m tea.Model, opts ...tea.ProgramOption) error {
	p := tea.NewProgram(m, opts...)
	_, err := p.Run()
	return err //nolint:wrapcheck // tea error is already descriptive
}

// RunInline runs the program in inline mode (no alternate screen). Since the
// alternate screen is now opt-in via the model's [tea.View], this is a thin
// alias of Run kept for API symmetry.
func RunInline(m tea.Model, opts ...tea.ProgramOption) error {
	p := tea.NewProgram(m, opts...)
	_, err := p.Run()
	return err //nolint:wrapcheck // tea error is already descriptive
}

// Quit returns a tea.Cmd that immediately sends a tea.QuitMsg.
// Useful as a shorthand in model Update methods.
func Quit() tea.Cmd { return tea.Quit }
