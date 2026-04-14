# terraform/modules/bucket

Object-store bucket with sane defaults: AES-256 at rest, public access blocked, optional versioning + lifecycle.

## Usage

```hcl
module "storage" {
  source = "github.com/golusoris/golusoris//deploy/terraform/modules/bucket?ref=v0.1.0"

  name        = "myapp-prod-storage"
  versioning  = true
  expire_days = 90

  tags = {
    app         = "myapp"
    environment = "prod"
  }
}
```

## Provider

AWS S3 by default. For GCS or Azure Blob, fork this module and swap the provider/resource block — the variable + output API is intentionally provider-agnostic so downstream code (e.g. an app's `storage/` backend) doesn't change when you migrate.
