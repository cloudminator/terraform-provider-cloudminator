# Terraform / OpenTofu provider for Cloudminator

Manage resources on the [Cloudminator](https://cloudminator.com) sovereign cloud platform
with Terraform or OpenTofu: projects, SSH keys, object-storage buckets and access keys,
VPCs, subnets, firewall rules, virtual machines and secrets.

```hcl
terraform {
  required_providers {
    cloudminator = {
      source = "cloudminator/cloudminator"
    }
  }
}

provider "cloudminator" {} # CLOUDMINATOR_ENDPOINT, CLOUDMINATOR_API_KEY, CLOUDMINATOR_ORG

resource "cloudminator_bucket" "backups" {
  name = "acme-backups"
}
```

Full documentation is in [`docs/`](docs/) and on the registry once published. A complete
stack is in [`examples/complete`](examples/complete/main.tf).

## What is offered

| Resource | Changes in place | Everything else |
| --- | --- | --- |
| `cloudminator_project` | `name` | replaces |
| `cloudminator_ssh_key` | — | replaces |
| `cloudminator_bucket` | `tags`, `quota_bytes`, `max_objects` | replaces |
| `cloudminator_bucket_key` | — | replaces |
| `cloudminator_vpc` | `name`, `tags` | replaces |
| `cloudminator_subnet` | `name`, `tags`, `snat`, `dhcp` | replaces |
| `cloudminator_firewall` | `name`, `tags`, `protocol`, `port`, `source`, `comment` | replaces (`subnet_id`) |
| `cloudminator_vm` | `name`, `tags`, `size`, growing `disk_gb`, `running` | replaces |
| `cloudminator_secret` | `name`, `tags`, `data` (new version) | replaces |

Data sources: `cloudminator_regions`, `cloudminator_flavors`, `cloudminator_images`,
`cloudminator_plans`.

Clusters, managed databases and containers are not offered yet: the platform does not
offer what has no real backend behind it (platform ADR 0058). Fields the platform stores
but does not act on (bucket `versioning`/`website_host`, VPC `zone_type`, subnet `zone`,
secret `engine`) are deliberately left out.

## How it fits the platform

The provider talks to the platform's uniform resource API (platform ADR 0009) and is
specified in platform ADR 0062:

- create: `POST /v1/orgs/{org}/projects/{project}/{kind}` with a fresh `Idempotency-Key`,
  then poll `status.phase` until `Running`;
- update: `POST …/{id}:update` (plus `:resize`, `:start`, `:stop` for VMs);
- delete: `DELETE …/{id}`, then poll until `404`.

`internal/contract/openapi.public.json` is a copy of the platform's public contract
(`GET /v1/openapi.json`). `TestSchemasMatchContract` checks every resource schema against
it — required/computed/sensitive flags and which fields change in place. Refresh it with:

```shell
make contract                                  # from $CLOUDMINATOR_ENDPOINT/v1/openapi.json
make contract CONTRACT_SRC=../cloudminator-com-platform/docs/openapi.public.json
```

When the platform makes a field updatable the test fails until the field is added to the
resource's `…Updatable` set — that is the whole change.

## Development

Requires Go (see `go.mod`) and Terraform or OpenTofu on `PATH`.

```shell
make build             # compile
make test              # unit tests, incl. full plan/apply/import against an in-memory fake platform
make lint              # golangci-lint
make generate          # format examples + regenerate docs/ (CI fails if docs/ is stale)
make validate-examples # terraform validate every example against the local build
```

Run the unit tests against OpenTofu instead of Terraform:

```shell
TF_ACC_TERRAFORM_PATH=$(command -v tofu) TF_ACC_PROVIDER_HOST=registry.opentofu.org go test ./...
```

Acceptance tests create **real** resources and only run with `TF_ACC=1`. Point them at a
disposable organization on a non-production platform, never a customer organization:

```shell
export CLOUDMINATOR_ENDPOINT=… CLOUDMINATOR_API_KEY=cm_… CLOUDMINATOR_ORG=…
make testacc
```

To use a local build from a Terraform configuration, add a `dev_overrides` block for
`cloudminator/cloudminator` to your CLI configuration pointing at `$(go env GOPATH)/bin`
and run `make install`.

## Releasing

1. Update `CHANGELOG.md`.
2. Push a tag `vX.Y.Z` on `main` (a human step).
3. `.github/workflows/release.yml` runs GoReleaser: zips per OS/arch, `SHA256SUMS` and its
   detached GPG signature (key `3C76B25E1C84D51EB50055563703FE4226A42291`, from the
   `GPG_PRIVATE_KEY` / `PASSPHRASE` repository secrets), and the registry manifest.
4. First release only: add the provider at registry.terraform.io (with the public key) and
   submit it to the OpenTofu registry. Later releases are picked up automatically.

## License

[MPL-2.0](LICENSE)
