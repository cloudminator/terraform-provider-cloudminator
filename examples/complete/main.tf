# A complete stack: a project with a network, a VM on it, an HTTPS rule,
# a bucket with an access key, and a secret.

terraform {
  required_providers {
    cloudminator = {
      source = "cloudminator/cloudminator"
    }
  }
}

# endpoint, api_key and org come from the CLOUDMINATOR_* environment variables.
provider "cloudminator" {}

variable "org" {
  type    = string
  default = "acme"
}

variable "ssh_public_key" {
  type = string
}

resource "cloudminator_project" "app" {
  org  = var.org
  name = "App"
}

data "cloudminator_regions" "all" {}

locals {
  region  = [for r in data.cloudminator_regions.all.regions : r.id if r.capabilities.compute][0]
  project = cloudminator_project.app.slug
}

resource "cloudminator_ssh_key" "deploy" {
  org        = var.org
  name       = "deploy"
  public_key = var.ssh_public_key
}

resource "cloudminator_vpc" "main" {
  project = local.project
  name    = "main"
  region  = local.region
}

resource "cloudminator_subnet" "app" {
  project = local.project
  name    = "app"
  vpc_id  = cloudminator_vpc.main.id
}

resource "cloudminator_firewall" "https" {
  project   = local.project
  name      = "https"
  subnet_id = cloudminator_subnet.app.id
  protocol  = "tcp"
  port      = "443"
}

resource "cloudminator_vm" "web" {
  project   = local.project
  name      = "web-1"
  image     = "debian-13"
  size      = "2c-4g"
  subnet_id = cloudminator_subnet.app.id
  ssh_keys  = [cloudminator_ssh_key.deploy.public_key]
}

resource "cloudminator_bucket" "assets" {
  project = local.project
  name    = "${var.org}-app-assets"
}

resource "cloudminator_bucket_key" "app" {
  project = local.project
  bucket  = cloudminator_bucket.assets.id
}

resource "cloudminator_secret" "s3" {
  project = local.project
  name    = "app-s3"
  data = {
    access_key_id     = cloudminator_bucket_key.app.access_key_id
    secret_access_key = cloudminator_bucket_key.app.secret_access_key
  }
}

output "web_address" {
  value = cloudminator_vm.web.address
}
