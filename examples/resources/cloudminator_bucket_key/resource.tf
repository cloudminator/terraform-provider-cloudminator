resource "cloudminator_bucket" "backups" {
  name = "acme-backups"
}

resource "cloudminator_bucket_key" "restic" {
  bucket = cloudminator_bucket.backups.id
  name   = "restic"
}

output "access_key_id" {
  value = cloudminator_bucket_key.restic.access_key_id
}

output "secret_access_key" {
  value     = cloudminator_bucket_key.restic.secret_access_key
  sensitive = true
}
