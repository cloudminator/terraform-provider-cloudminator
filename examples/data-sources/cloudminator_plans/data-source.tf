data "cloudminator_plans" "all" {}

output "vm_limit_per_plan" {
  value = { for p in data.cloudminator_plans.all.plans : p.code => lookup(p.resource_limits, "vms", null) }
}
