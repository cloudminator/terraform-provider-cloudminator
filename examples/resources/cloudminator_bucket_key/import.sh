# Import a bucket key by organization, project, bucket and access key id.
# The secret is only returned at creation, so an imported key has no secret_access_key.
terraform import cloudminator_bucket_key.restic acme/default/acme-backups/GK0123456789abcdef
