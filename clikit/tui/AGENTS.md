# Agent guide — clikit/tui/

Thin wrappers over charmbracelet/bubbletea v2 (`charm.land/bubbletea/v2`) for
building terminal UIs.

## Usage

```go
// Implement tea.Model:
type model struct{ choice string; done bool }
func (m model) Init() tea.Cmd { return nil }
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    if k, ok := msg.(tea.KeyMsg); ok && k.String() == "enter" {
        m.done = true
        return m, tea.Quit
    }
    return m, nil
}
func (m model) View() tea.View {
    v := tea.NewView("Press Enter to continue\n")
    v.AltScreen = true // full-screen is opt-in on the view in v2
    return v
}

if err := tui.Run(model{}); err != nil { ... }
```

## bubbletea v2 notes

- `View()` now returns `tea.View` (was `string`). Build it with
  `tea.NewView(content)`.
- AltScreen and mouse tracking are no longer program options. The model enables
  them via `View.AltScreen` / `View.MouseMode`, so `tui.Run` cannot inject them.
- `tui.Run` and `tui.RunInline` are now equivalent thin wrappers; `RunInline`
  is kept only for API symmetry.

## Don't

- Don't call `tea.NewProgram` directly — use `tui.Run` for the standard error
  handling.
