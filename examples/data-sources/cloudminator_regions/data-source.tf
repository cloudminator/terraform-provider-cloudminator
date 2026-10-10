data "cloudminator_regions" "all" {}

output "compute_regions" {
  value = [for r in data.cloudminator_regions.all.regions : r.id if r.capabilities.compute]
}
