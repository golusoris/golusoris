<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ADR-0018: Relicense to EUPL-1.2 (code) + CC-BY-SA-4.0 (prose), REUSE + DCO

- **Status**: Accepted
- **Date**: 2026-09-14
- **Deciders**: @lusoris
- **Tags**: licensing, compliance, governance

## Context

golusoris shipped under MIT since its first commit. The sibling projects the
framework now onboards — cordanaLLM/Aegis-OS and cordanaLLM/praetor — use the
European Union Public Licence 1.2 for code and CC BY-SA 4.0 for prose, with a
REUSE-compliant layout and a Developer Certificate of Origin gate. The
framework's own compliance anchors (principles.md §2.5: EU CRA, NIS2, GDPR,
EU AI Act) favour a licence drafted for EU law. All human commits to date are
by the copyright holder, so a relicense needs no third-party consent.

## Decision

- Code, configuration, tooling and deploy manifests: **EUPL-1.2**
  (`LICENSE` is the EUPL text).
- Documentation, `AGENTS.md` files, changelogs, governance texts:
  **CC-BY-SA-4.0**.
- Third-party material keeps its own licence with explicit `REUSE.toml`
  carve-outs: vendored agent skills (MIT / Apache-2.0), the Contributor
  Covenant (CC-BY-4.0), the eBPF program in `pkg/sockmap/bpf`
  (GPL-2.0-only, required for GPL-gated kernel helpers).
- Every first-party file carries an SPDX header (`reuse annotate` layout);
  `goheader` lints Go files, `reuse lint` gates the tree in CI.
- Contributions are inbound = outbound and must be signed off (DCO); CI
  rejects PR commits without `Signed-off-by`.
- Releases ≤ v0.8.0 remain MIT; v0.9.0 onward are EUPL-1.2.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| Stay MIT | Zero friction, maximal adoption | No share-alike; diverges from the fleet's licence, complicating governance tooling | Fleet consistency and EU-law fit outweigh it |
| Apache-2.0 | Patent grant, widely understood | Still permissive, still divergent from the fleet | Same as above |
| Single licence (EUPL for prose too) | Simpler | EUPL is a software licence; CC BY-SA is the right instrument for documentation and matches Aegis-OS | Follow the fleet split |
| Per-file headers only for Go | Smaller diff | REUSE compliance needs every file covered; `REUSE.toml` + headers is the audited path | Full annotation |

## Consequences

- **Positive**: one licence story across the fleet; machine-readable SBOM
  licence data; EUPL's compatibility appendix keeps GPL/AGPL/MPL combinations
  legal.
- **Negative**: copyleft — downstream apps that are Derivative Works must
  license under EUPL-1.2 or a compatible licence; MIT-only consumers must
  pin ≤ v0.8.0 or accept the terms. ~1 000 files touched by headers (one-off).
- **Neutral / follow-ups**: `golusoris init` scaffolds `LICENSE` + `REUSE.toml`
  + headers for new apps; LICENSING.md is the human-readable map.

## References

- [`LICENSING.md`](../../LICENSING.md) · [`REUSE.toml`](../../REUSE.toml) · [`LICENSES/`](../../LICENSES/)
- EUPL-1.2 text and compatibility appendix: <https://joinup.ec.europa.eu/collection/eupl>
- REUSE specification 3.3: <https://reuse.software/spec/>
- Developer Certificate of Origin: <https://developercertificate.org/>
