# Agent guide — clikit/tui/

Thin wrappers over charmbracelet/bubbletea for building terminal UIs.

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
func (m model) View() string { return "Press Enter to continue\n" }

// Full-screen:
if err := tui.Run(model{}); err != nil { ... }

// Inline (no alt-screen):
if err := tui.RunInline(model{}); err != nil { ... }
```

## Don't

- Don't call `tea.NewProgram` directly — use `tui.Run` / `tui.RunInline` for
  consistent defaults (AltScreen, mouse support).
