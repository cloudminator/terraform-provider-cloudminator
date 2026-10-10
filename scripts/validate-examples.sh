#!/usr/bin/env bash
# Validate the example configurations against a locally built provider.
# Usage: scripts/validate-examples.sh [terraform|tofu]
set -euo pipefail
cli="${1:-terraform}"
root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

go build -o "$work/bin/terraform-provider-cloudminator" "$root"
cat > "$work/cli.tfrc" <<RC
provider_installation {
  dev_overrides {
    "cloudminator/cloudminator" = "$work/bin"
    "registry.terraform.io/cloudminator/cloudminator" = "$work/bin"
    "registry.opentofu.org/cloudminator/cloudminator" = "$work/bin"
  }
  direct {}
}
RC
export TF_CLI_CONFIG_FILE="$work/cli.tfrc"

dirs=("$root/examples/provider" "$root/examples/complete")
for d in "$root"/examples/data-sources/*/; do dirs+=("$d"); done

status=0
for d in "${dirs[@]}"; do
  copy="$work/cfg/$(basename "$d")"
  mkdir -p "$copy" && cp "$d"/*.tf "$copy"/
  if ! grep -q required_providers "$copy"/*.tf; then
    printf 'terraform {\n  required_providers {\n    cloudminator = { source = "cloudminator/cloudminator" }\n  }\n}\n' > "$copy/_versions.tf"
  fi
  if (cd "$copy" && "$cli" validate -no-color >/dev/null 2>"$copy.err"); then
    echo "ok    $d"
  else
    echo "FAIL  $d"; cat "$copy.err"; status=1
  fi
done
exit $status
