<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — clikit/

Cobra + fx-aware CLI builder. Wires cobra sub-commands into fx lifecycle
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

## Completions + man pages (#630)

| Symbol | Purpose |
| --- | --- |
| `EnumFlag(cmd, name, values...)` | declare flag value set; shells complete it, man page lists it |
| `WriteCompletion(w, root, shell)` | one script; `Shells()` = bash, zsh, fish, powershell |
| `ManPages(root, ManOptions)` | one man(7) page per visible command; aliases + enum values listed |
| `Generate(root, ManOptions)` | completions + man pages, keyed `completions/<shell>/...`, `man/man<N>/...` |
| `WriteFiles(dir, files)` / `CheckDrift(dir, files)` | write output; CI drift check -> `ErrDrift` names stale, missing, unexpected files |

- Scripts call `<prog> __complete` at TAB time; flags never baked into scripts, so scripts cannot drift.
- Output byte-reproducible: no date unless `ManOptions.Date` set. Golden: `testdata/golden`, regenerate via `GOLUSORIS_UPDATE_GOLDEN=1 go test ./clikit/`.
- Tree walk iterative, bounded by `MaxCommands`; `CheckDrift` walk bounded by `MaxGeneratedFiles`.
- `ManPages`/`Generate` add cobra default `completion` command + help flags, same as `Execute` does.
- Shell harness test runs real bash, zsh, fish when installed; PowerShell covered via `__complete` protocol test only.

## Sub-package

- `clikit/tui/` — bubbletea helpers (`Run`, `RunInline`, `Quit`)

## Don't

- Don't call `os.Exit` inside `WithRunE` — return error instead.
- Don't mix `WithFx` and `WithRunE` on same command (WithRunE takes precedence).
