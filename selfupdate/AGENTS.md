<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — selfupdate/

Purpose: verified binary self-update from GitHub releases via `minio/selfupdate`.

## Usage

```go
result, err := selfupdate.Update(ctx, selfupdate.Options{
    Owner:             "golusoris",
    Repo:              "myapp",
    Version:           version.Read().Version,
    MaxAssetBytes:     128 << 20,
    OperationTimeout:  10 * time.Minute,
    PublisherVerifier: selfupdate.NewPublisherVerifier(verifyPublisher),
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

- Default: exact `<repo>_<version>_<GOOS>_<GOARCH>.tar.gz`.
- Windows: exact `.zip` suffix.
- `Options.AssetName`: exact archive override. No prefix or case-folded match.
- `Options.BinaryName`: exact flat archive member override.
- Empty `BinaryName`: running executable basename.
- Archive member: one regular executable only; bounded size and entry count.

## Publisher and checksum verification

- `checksums.txt`: mandatory exact release asset.
- `checksums.txt.sigstore.json`: mandatory exact release asset.
- `Options.PublisherVerifier`: mandatory when newer release exists; construct
  it with `NewPublisherVerifier` so `Options` remains comparable.
- Verifier trust policy: pin OIDC issuer and workflow identity. Tag-scoped
  identity regex must anchor repository, workflow path, and tag ref.
- Verifier contract: bind bundle to raw manifest bytes; validate signature,
  certificate chain, pinned identity, SCT, transparency inclusion, and time.
- Runtime: no implicit CLI, trust-root fetch, or hidden network request.
- HTTP status: `200 OK` mandatory.
- Selected archive filename: exact manifest match.
- Digest: one valid SHA-256 only.
- Missing, unavailable, malformed, duplicate, untrusted, or mismatched input:
  update denied before archive download.
- Manifest bound: 4 MiB and 512 lines. Bundle bound: 4 MiB.

## Version rule

- Installed version and release tag: strict semantic versions with numeric
  `MAJOR.MINOR.PATCH`; prerelease and build suffixes plus optional lowercase
  `v` prefix are supported.
- Equal precedence: no update.
- Older release: explicit downgrade refusal.
- Invalid or development version: update denied.

## Download bound

- `Options.MaxAssetBytes`: downloaded archive and extracted binary cap.
- Zero: `DefaultMaxAssetBytes` (256 MiB).
- Negative: invalid.

## Operation deadline

- `Options.OperationTimeout`: bounds release lookup, verification, download,
  extraction, and entry into executable replacement as one operation.
- Zero: `DefaultOperationTimeout` (10 minutes).
- Negative: invalid. Earlier caller deadline always wins.
- Cancellation: checked throughout extraction and before replacement starts.
- Replacement started: finish commit or rollback. Cancellation cannot preempt
  the atomic filesystem transition safely.

## Don't

- No unbounded `Update`; keep default or set shorter operation timeout.
- No verifier that skips artifact binding or identity checks.
- No prefix asset matching.
- No direct archive bytes into `minio/selfupdate.Apply`.
- Check `result.Updated`; caller restart required.
