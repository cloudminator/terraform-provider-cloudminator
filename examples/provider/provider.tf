terraform {
  required_providers {
    cloudminator = {
      source = "cloudminator/cloudminator"
    }
  }
}

# Credentials are best supplied through the environment:
#   CLOUDMINATOR_ENDPOINT, CLOUDMINATOR_API_KEY, CLOUDMINATOR_ORG, CLOUDMINATOR_PROJECT
provider "cloudminator" {
  endpoint = "https://platform.example.com"
  org      = "acme"
  project  = "default"
}
