# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

variable "name" {
  description = "Bucket name (DNS-compliant; globally unique on S3)."
  type        = string
}

variable "versioning" {
  description = "Enable object versioning."
  type        = bool
  default     = true
}

variable "expire_days" {
  description = "Non-current version expiration (days). 0 disables lifecycle."
  type        = number
  default     = 90
}

variable "force_destroy" {
  description = "Allow terraform destroy to remove non-empty buckets (dev only)."
  type        = bool
  default     = false
}

variable "kms_key_arn" {
  description = "Existing customer-managed KMS key ARN. Null creates a dedicated rotating key."
  type        = string
  default     = null
  nullable    = true

  validation {
    condition     = var.kms_key_arn == null || startswith(var.kms_key_arn, "arn:")
    error_message = "kms_key_arn must be null or an AWS ARN."
  }
}

variable "tags" {
  description = "Tags to attach to the bucket."
  type        = map(string)
  default     = {}
}
