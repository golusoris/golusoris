# Agent guide — clikit/

Cobra + fx-aware CLI builder. Wires cobra sub-commands into the fx lifecycle
so long-running commands call `app.Run()` naturally while one-shot commands
skip fx entirely.

## Usage

```go
root := clikit.New("myapp", "My application")

root.AddCommand(
    // fx-backed long-running command:
    clikit.Command("serve", "Start HTTP server",
        clikit.WithFx(core.Module, httpx.Module),
    ),
    // Plain one-shot command (no fx):
    clikit.Command("version", "Print version",
        clikit.WithRunE(func(cmd *cobra.Command, _ []string) error {
            _, _ = fmt.Fprintln(cmd.OutOrStdout(), version.Current)
            return nil
        }),
    ),
)

if err := root.Execute(); err != nil {
    os.Exit(1)
}
```

## WithRunHook — one-shot fx actions

```go
clikit.Command("migrate", "Run DB migrations",
    clikit.WithFx(db.Module),
    clikit.WithRunHook(func(app *fx.App) {
        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        if err := app.Start(ctx); err != nil { ... }
        // do work ...
        _ = app.Stop(ctx)
    }),
)
```

## Sub-package

- `clikit/tui/` — bubbletea helpers (`Run`, `RunInline`, `Quit`)

## Don't

- Don't call `os.Exit` inside `WithRunE` — return an error instead.
- Don't mix `WithFx` and `WithRunE` on the same command (WithRunE takes precedence).
