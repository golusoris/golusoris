variable "name" {
  description = "Identifier (used for the DB instance, SG, subnet group)."
  type        = string
}

variable "vpc_id" {
  description = "VPC to place the DB in."
  type        = string
}

variable "subnet_ids" {
  description = "Private subnets for the DB subnet group (≥ 2 for Multi-AZ)."
  type        = list(string)
}

variable "allowed_security_groups" {
  description = "Security groups allowed to connect to Postgres."
  type        = list(string)
  default     = []
}

variable "allowed_cidrs" {
  description = "Additional CIDR ranges allowed to connect (prefer SGs in prod)."
  type        = list(string)
  default     = []
}

variable "engine_version" {
  description = "Postgres engine major.minor (e.g. 17.2)."
  type        = string
  default     = "17.2"
}

variable "instance_class" {
  description = "RDS instance class (e.g. db.t4g.small / db.m6g.large)."
  type        = string
  default     = "db.t4g.small"
}

variable "allocated_storage" {
  description = "Initial storage (GiB)."
  type        = number
  default     = 20
}

variable "max_allocated_storage" {
  description = "Storage autoscaling ceiling (GiB). 0 disables autoscaling."
  type        = number
  default     = 100
}

variable "database" {
  description = "Initial database name."
  type        = string
  default     = "app"
}

variable "username" {
  description = "Master username."
  type        = string
  default     = "app"
}

variable "multi_az" {
  description = "Enable Multi-AZ failover."
  type        = bool
  default     = true
}

variable "backup_retention_days" {
  description = "Automated backup retention (0–35 days)."
  type        = number
  default     = 7
}

variable "deletion_protection" {
  description = "Block accidental destroy in prod."
  type        = bool
  default     = true
}

variable "skip_final_snapshot" {
  description = "Skip final snapshot on delete (dev only)."
  type        = bool
  default     = false
}

variable "apply_immediately" {
  description = "Apply parameter changes immediately (default: next maintenance window)."
  type        = bool
  default     = false
}

variable "tags" {
  description = "Tags attached to all resources."
  type        = map(string)
  default     = {}
}
