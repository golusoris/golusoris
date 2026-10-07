<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — storage/azblob/

Azure Blob Storage backend (block blobs): `storage.Bucket` +
`storage.Copier` + `storage.PutPresigner`. Own `go.mod` (azblob container
package pulls Apache Arrow; azidentity pulls MSAL; root graph stays lean).
Replaces `../..` + `../../core`. No root SDK package `azblob` imported (name
clash); sub-packages `blob`/`blockblob`/`container`/`service`/`sas` only.

## Wiring

```go
fx.New(golusoris.Core, azblob.Module) // provides storage.Bucket; replaces storage.Module
```

Needs `*config.Config`, `*slog.Logger`, `clock.Clock`. `New` does no I/O;
credentials resolve on first request. Debug log names auth path
(`entra-id` / `shared-key`).

```
storage.azblob.service_url  = "https://acct.blob.core.windows.net/"
storage.azblob.container    = "uploads"
storage.azblob.account_name = ""   # + account_key -> shared key (Azurite)
storage.azblob.account_key  = ""
storage.azblob.client_id    = ""   # WI app / user-assigned MI
storage.azblob.tenant_id    = ""   # WI tenant override
storage.azblob.federated_token_file = ""  # WI token override
storage.azblob.presign_ttl  = 15m  # URL() lifetime, max 7d
storage.azblob.block_size   = 8388608  # 1 MiB..4000 MiB
storage.azblob.concurrency  = 5        # 1..32 (SDK default is CPU-scaled: always set)
```

## Credentials + SAS

- No account key -> Entra ID chain: WorkloadIdentityCredential (only when
  `federated_token_file` or `AZURE_FEDERATED_TOKEN_FILE` set; explicit
  misconfig = error) -> ManagedIdentityCredential (`client_id` = user-assigned).
  No CLI/dev credentials, no `DefaultAzureCredential`.
- SAS: account key -> service SAS (shared key). Entra ID -> user delegation
  SAS; one Get User Delegation Key per URL, key window = [now-1m, SAS expiry],
  bounded by caller ctx + 30s. Identity needs
  `Microsoft.Storage/storageAccounts/blobServices/generateUserDelegationKey`
  at account scope or above (Storage Blob Data Contributor includes it).
- Expiry: `se` is whole seconds; rounded up so URL lives >= ttl, capped at
  floor(now+7d) (delegation key ceiling). `Expires` = `se`.
- `PresignPut`: `cw` permission scoped to one blob. Enforced: key, expiry,
  permission, MD5 (`Content-MD5`). Applied, not enforced: content type
  (`x-ms-blob-content-type`), metadata (`x-ms-meta-*`). Header
  `x-ms-blob-type: BlockBlob` required. Length -> `ErrUnsupportedConstraint`;
  CRC32C/SHA-256 -> `ErrUnsupportedChecksum`. SDK's newer signed request
  headers (`srh`, user delegation only) unused: not exercised against any
  emulator yet; candidate follow-up for header binding.

## Semantics

- `Put`: UploadStream, `concurrency` x `block_size` buffers, commit block
  list. Unseekable + unknown length OK (50000-block cap). Metadata names =
  C# identifiers, checked before upload; stored lowercase.
- `Get`: one Get Blob returns body + attributes. Metadata keys lowercased
  (HTTP canonicalisation mangles case).
- `Delete`: includes snapshots; missing = nil.
- `Copy`: Get Properties -> Copy Blob pinned `SourceIfMatch` ETag -> poll
  pending status every 500ms via `clock.After`, max 7200 polls (1h) or ctx.
- `List`: one List Blobs page, `MaxResults` = limit.

## Tests

- `internal_test.go`: option bounds, SAS headers, metadata names, expiry
  rounding, user delegation via TLS httptest, credential chain build, copy
  polling with fake clock.
- `azblob_test.go`: `testutil/objstore.StartAzurite` + `RunConformance`
  (`PresignEnforced`, `URLFetchable`), staged-block unseekable upload, MD5
  enforcement.
