# Import a secret by organization, project and secret id.
# The platform never returns secret material, so an imported secret has no data
# until Terraform writes it (which creates a new version).
terraform import cloudminator_secret.db acme/default/secret-0123456789abcdef01234567
