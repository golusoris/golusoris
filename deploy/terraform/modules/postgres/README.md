# terraform/modules/postgres

Managed Postgres via AWS RDS with production defaults: Multi-AZ, encrypted storage, gp3, Performance Insights, 7-day backups, IAM auth enabled.

## Usage

```hcl
module "db" {
  source = "github.com/golusoris/golusoris//deploy/terraform/modules/postgres?ref=v0.1.0"

  name           = "myapp-prod"
  vpc_id         = module.network.vpc_id
  subnet_ids     = module.network.private_subnet_ids
  engine_version = "17.2"
  instance_class = "db.m6g.large"
  database       = "myapp"
  multi_az       = true

  allowed_security_groups = [module.app.security_group_id]

  tags = { app = "myapp", environment = "prod" }
}

output "dsn" {
  value     = module.db.dsn
  sensitive = true
}
```

## Outputs

- `host` / `port` / `database` / `username` — stable across provider swaps.
- `password` (sensitive) — random 32-char string; rotate via RDS manual rotation or secrets-manager integration.
- `dsn` (sensitive) — full Postgres URL for the app's `APP_DB_DSN`.

## Other providers

Fork this module and swap the `aws_db_instance` block for `google_sql_database_instance`, `azurerm_postgresql_flexible_server`, `digitalocean_database_cluster`, etc. Keep the variable + output API identical so downstream Helm values don't change.
