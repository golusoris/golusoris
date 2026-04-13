# Security policy

## Reporting a vulnerability

Please **do not** open public GitHub issues for security vulnerabilities.

Email: `security@lusoris.dev` (or open a private security advisory on GitHub).

We aim to acknowledge within 72 hours and provide a remediation plan within 7 days.

## Supported versions

Only the latest minor release is patched. Apps should bump promptly when an advisory is published.

## Supply chain

Releases are:
- Built reproducibly in CI from tagged source
- Signed with [cosign](https://docs.sigstore.dev/cosign/) (keyless, GitHub OIDC)
- Accompanied by a [syft](https://github.com/anchore/syft) SBOM
- Attested with [SLSA](https://slsa.dev/) L3 provenance

Verify a release container:

```bash
cosign verify ghcr.io/golusoris/golusoris:vX.Y.Z \
  --certificate-identity-regexp '^https://github.com/golusoris/golusoris/' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
```

## Dependencies

Tracked by Renovate (routine bumps) + Dependabot (security alerts). Auto-merge on green CI for minor/patch; majors require human review.
