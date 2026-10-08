<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# container/registry/credentials/ecr

Amazon ECR provider for `container/registry/credentials` chain.

```go
p, err := ecr.NewDefault(ctx)      // AWS default chain: env, IRSA, Pod Identity, IMDS
p := ecr.New(awsCfg, optFns...)    // explicit aws.Config / ECR client options
region, ok := ecr.Region(host)     // host -> region, false for non-ECR
fx.New(..., credentials.Module, ecr.Module)
```

- Region from host; one `GetAuthorizationToken` per token lifetime (`credentials.Cache`, 12h tokens).
- Token `base64(user:password)`; malformed -> `ErrToken`.
- Non-ECR host -> `credentials.ErrNoCredential`.
- No config keys; AWS SDK env/profile config applies.

## Tests

- Fake ECR endpoint via `ecr.Options.BaseEndpoint`.
- IRSA path: fake STS + ECR via `AWS_ENDPOINT_URL_STS` / `AWS_ENDPOINT_URL_ECR`, loopback-only HTTP client.
- SDK quirk: plain `AWS_ENDPOINT_URL` does not reach STS web identity client (credentials resolve before base endpoint); service-specific vars do.
