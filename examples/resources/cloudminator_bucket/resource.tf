resource "cloudminator_bucket" "backups" {
  name        = "acme-backups"
  region      = "eu-central-1"
  quota_bytes = 500 * 1024 * 1024 * 1024 # 500 GiB

  tags = {
    env = "prod"
  }
}

output "s3_endpoint" {
  value = cloudminator_bucket.backups.endpoint
}

# Address the bucket from S3 clients by s3_name, not name.
output "s3_bucket" {
  value = cloudminator_bucket.backups.s3_name
}
