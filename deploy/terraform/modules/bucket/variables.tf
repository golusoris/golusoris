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

variable "tags" {
  description = "Tags to attach to the bucket."
  type        = map(string)
  default     = {}
}
