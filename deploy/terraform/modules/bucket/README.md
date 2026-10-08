<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# terraform/modules/bucket

Object-store bucket with customer-managed KMS encryption, public access blocked, optional versioning, and lifecycle cleanup.

## Usage

```hcl
module "storage" {
  source = "github.com/golusoris/golusoris//deploy/terraform/modules/bucket?ref=v0.12.0"

  name        = "myapp-prod-storage"
  versioning  = true
  expire_days = 90

  tags = {
    app         = "myapp"
    environment = "prod"
  }
}
```

Omitting `kms_key_arn` creates a dedicated rotating KMS key. Set it to an
existing customer-managed key ARN when applications share key administration.

## Provider

AWS S3 by default. For GCS or Azure Blob, fork this module and swap the provider/resource block — the variable + output API is intentionally provider-agnostic so downstream code (e.g. an app's `storage/` backend) doesn't change when you migrate.
