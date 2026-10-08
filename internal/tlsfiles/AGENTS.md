<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — internal/tlsfiles/

Client `*tls.Config` from PEM files for broker clients (`pubsub/nats`,
`pubsub/kafka`). Reads files once; no reload.

## Contract

- `ClientConfig(Files{CA, Cert, Key})`: TLS 1.2 floor. CA -> `RootCAs`;
 no CA -> system roots. Cert + Key -> client certificate.
- `ErrPartialPair`: cert without key or key without cert.
 `ErrEmptyCA`: CA file without PEM certificate.
- `tlsfilestest.Write(t)`: throwaway CA + client cert + key in `t.TempDir()`.

## Don't

- Don't grow reload logic here. Rotation belongs in `core/tlsx` reloader;
 inject its config (`nats.ProvideTLSConfig`) instead.
