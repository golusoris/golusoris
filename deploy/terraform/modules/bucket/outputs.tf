output "name" {
  description = "Bucket name."
  value       = aws_s3_bucket.this.id
}

output "arn" {
  description = "Bucket ARN (useful for IAM policies)."
  value       = aws_s3_bucket.this.arn
}

output "regional_domain_name" {
  description = "Region-qualified S3 hostname (for presigned URLs)."
  value       = aws_s3_bucket.this.bucket_regional_domain_name
}
