data "cloudminator_flavors" "all" {}

locals {
  small = [for f in data.cloudminator_flavors.all.flavors : f if f.vcpus == 2][0]
}

output "small_size" {
  value = local.small.size
}
