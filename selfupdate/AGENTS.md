# Agent guide — selfupdate/

Binary self-update from GitHub releases via `minio/selfupdate`.

Fetches the latest release from the GitHub API, selects the asset matching
`<repo>_<os>_<arch>` (customisable), verifies SHA-256 checksum when a
`*_checksums.txt` asset is present, and replaces the running executable atomically.

## Usage

```go
result, err := selfupdate.Update(ctx, selfupdate.Options{
    Owner:   "golusoris",
    Repo:    "myapp",
    Version: version.Current, // e.g. "v1.2.3"
})
if err != nil {
    log.Fatal(err)
}
if result.Updated {
    fmt.Printf("Updated %s → %s. Restart to use the new version.\n",
        result.CurrentVersion, result.LatestVersion)
}
```

## Asset naming

The default heuristic matches assets whose name starts with `<repo>_<GOOS>_<GOARCH>`.
goreleaser's default `{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}` is compatible.

Override with `Options.AssetName` for non-standard naming.

## Checksum verification

When the release contains a `*_checksums.txt` asset (goreleaser's default),
`Update` downloads it and cross-checks the SHA-256 of the binary before applying.
No configuration needed.

## Don't

- Don't call `Update` without a context timeout — GitHub API + asset download
  can be slow on poor connections.
- Don't skip the `result.Updated` check — the caller must restart the process;
  the new binary is on disk but not yet running.
