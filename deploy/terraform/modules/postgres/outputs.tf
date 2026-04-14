output "host" {
  description = "DB host (connect endpoint)."
  value       = aws_db_instance.this.address
}

output "port" {
  description = "DB port."
  value       = aws_db_instance.this.port
}

output "database" {
  description = "Initial database name."
  value       = aws_db_instance.this.db_name
}

output "username" {
  description = "Master username."
  value       = aws_db_instance.this.username
}

output "password" {
  description = "Master password (sensitive)."
  value       = random_password.db.result
  sensitive   = true
}

output "dsn" {
  description = "Postgres DSN (sensitive)."
  value       = "postgres://${aws_db_instance.this.username}:${random_password.db.result}@${aws_db_instance.this.address}:${aws_db_instance.this.port}/${aws_db_instance.this.db_name}?sslmode=require"
  sensitive   = true
}

output "security_group_id" {
  description = "DB security group ID (attach from app SG as ingress)."
  value       = aws_security_group.this.id
}
