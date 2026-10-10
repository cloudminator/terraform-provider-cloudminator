terraform {
  required_providers {
    cloudminator = {
      source = "cloudminator/cloudminator"
    }
  }
}

# Credentials are best supplied through the environment:
#   CLOUDMINATOR_API_KEY, CLOUDMINATOR_ORG, CLOUDMINATOR_PROJECT
# endpoint defaults to https://api.cloudminator.com (override with CLOUDMINATOR_ENDPOINT).
provider "cloudminator" {
  org     = "acme"
  project = "default"
}
