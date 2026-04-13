# Agent guide — httpx/vite

Parses Vite's `manifest.json` to resolve entry points to hashed asset URLs.

## Conventions

- Build step: `vite build` emits `manifest.json` with `isEntry` + hashed filenames + CSS/imports graph.
- Load once at app startup via `vite.NewFromFile` or `vite.NewFromFS` (for embed.FS).
- In templates: `{{ .Manifest.File "src/main.tsx" }}` and `{{ range .Manifest.CSS "src/main.tsx" }}`.
- `CSS(src)` follows the manifest's transitive imports so shared CSS from imported JS chunks comes along automatically.

## Don't

- Don't parse manifest.json on every request. Parse once, inject via fx, re-parse on SIGHUP if live-reload is needed.
