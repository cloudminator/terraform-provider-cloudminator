## 0.1.0 (Unreleased)

FEATURES:

* **New resources:** `cloudminator_project`, `cloudminator_ssh_key`, `cloudminator_bucket`,
  `cloudminator_bucket_key`, `cloudminator_vpc`, `cloudminator_subnet`,
  `cloudminator_firewall`, `cloudminator_vm`, `cloudminator_secret`.
* **New data sources:** `cloudminator_regions`, `cloudminator_flavors`,
  `cloudminator_images`, `cloudminator_plans`.
* Idempotent creates (`Idempotency-Key`), so a retried create never duplicates a resource.
* Works with Terraform and OpenTofu.
