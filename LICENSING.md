<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Licensing

golusoris is **fully open**: every first-party file is under an OSI/FSF-approved
licence, the repository is [REUSE](https://reuse.software)-compliant, and the
licence of any file is machine-readable from its SPDX header or from
[`REUSE.toml`](REUSE.toml). Full texts live in [`LICENSES/`](LICENSES/).

## Split by nature of material

| Material | Licence | SPDX id |
|---|---|---|
| Go source, tests, generated code, Makefiles, CI workflows, deploy manifests, tooling configs | [European Union Public Licence 1.2](LICENSES/EUPL-1.2.txt) | `EUPL-1.2` |
| Prose: `README.md`, `docs/**`, every `AGENTS.md`, `CLAUDE.md`, changelogs, governance texts | [Creative Commons Attribution-ShareAlike 4.0](LICENSES/CC-BY-SA-4.0.txt) | `CC-BY-SA-4.0` |
| `CODE_OF_CONDUCT.md` (adapted Contributor Covenant v2.1) | [CC BY 4.0](LICENSES/CC-BY-4.0.txt) | `CC-BY-4.0` |
| Vendored agent skills under `.claude/skills/superpowers/` and `.claude/skills/community/` | upstream MIT / Apache-2.0 — see each directory's `README.md` | `MIT`, `Apache-2.0` |

`LICENSE` at the repository root is the EUPL-1.2 text; it governs the code.

## Why the EUPL

- **Copyleft that composes.** EUPL-1.2 is a share-alike licence with an explicit
  compatibility list (Appendix), so golusoris code can be combined with
  GPL-2.0/3.0, AGPL-3.0, MPL-2.0, LGPL, EPL, OSL, CeCILL and others without a
  licence conflict. Apps that link the framework as a Go module are Derivative
  Works under the licence's own terms; consult the text and Appendix for your
  situation.
- **EU-law native.** Drafted and maintained by the European Commission, valid
  in all EU languages, with a clear applicable-law clause — a fit for a
  framework whose compliance anchors include the EU CRA, NIS2, GDPR and the
  EU AI Act (see [`docs/principles.md`](docs/principles.md) §2.5).
- **Same choice as the sibling projects** in the fleet (cordanaLLM/Aegis-OS,
  cordanaLLM/praetor), so governance tooling can reason about one licence.

## Contributing (inbound = outbound)

By contributing you agree that your contribution is licensed under the same
terms as the material it touches (EUPL-1.2 for code, CC-BY-SA-4.0 for prose)
and you certify the [Developer Certificate of Origin](https://developercertificate.org/)
by signing off every commit (`git commit -s`). CI checks the `Signed-off-by:`
trailer on pull requests. See [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Headers

Every first-party source file starts with:

```go
// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2
```

Prose files use the same two lines inside an HTML comment block with
`CC-BY-SA-4.0` (`reuse annotate` layout). Files with YAML front matter and
generated context files carry no header and are covered by `REUSE.toml`. Files that cannot carry comments (JSON, binary fixtures) are
covered by the aggregate annotations in `REUSE.toml`. `golangci-lint`
(`goheader`) enforces the Go header; `reuse lint` enforces the whole tree.

## History

The project was MIT-licensed from its first commit (2026-04-13) until the
switch to EUPL-1.2 + CC-BY-SA-4.0 on 2026-09-14 (ADR-0018). All human
contributions up to that point were by the copyright holder, so no third-party
consent was required. Releases up to and including `v0.8.0` remain available
under MIT; `v0.9.0` and later are EUPL-1.2.
