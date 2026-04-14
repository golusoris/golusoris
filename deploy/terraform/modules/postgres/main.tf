# Managed Postgres via AWS RDS. For CloudSQL / Azure Database / DO /
# Neon / Supabase / Fly, fork and swap the resource block; the
# input variables + outputs (host, port, database, username, password)
# stay stable so the app's DATABASE_URL template is unchanged.

terraform {
  required_version = ">= 1.6.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
  }
}

resource "random_password" "db" {
  length  = 32
  special = false
}

resource "aws_db_subnet_group" "this" {
  name       = "${var.name}-subnets"
  subnet_ids = var.subnet_ids
  tags       = var.tags
}

resource "aws_security_group" "this" {
  name        = "${var.name}-sg"
  description = "Postgres access for ${var.name}"
  vpc_id      = var.vpc_id
  tags        = var.tags

  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = var.allowed_security_groups
    cidr_blocks     = var.allowed_cidrs
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_db_instance" "this" {
  identifier     = var.name
  instance_class = var.instance_class
  engine         = "postgres"
  engine_version = var.engine_version

  allocated_storage     = var.allocated_storage
  max_allocated_storage = var.max_allocated_storage
  storage_type          = "gp3"
  storage_encrypted     = true

  db_name  = var.database
  username = var.username
  password = random_password.db.result

  db_subnet_group_name   = aws_db_subnet_group.this.name
  vpc_security_group_ids = [aws_security_group.this.id]
  publicly_accessible    = false

  multi_az                            = var.multi_az
  backup_retention_period             = var.backup_retention_days
  backup_window                       = "03:00-04:00"
  maintenance_window                  = "sun:04:00-sun:05:00"
  auto_minor_version_upgrade          = true
  iam_database_authentication_enabled = true

  skip_final_snapshot       = var.skip_final_snapshot
  final_snapshot_identifier = var.skip_final_snapshot ? null : "${var.name}-final-${formatdate("YYYYMMDD-hhmmss", timestamp())}"

  deletion_protection = var.deletion_protection
  apply_immediately   = var.apply_immediately

  performance_insights_enabled = true
  monitoring_interval          = 60

  tags = var.tags
}
