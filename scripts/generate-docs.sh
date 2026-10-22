#!/usr/bin/env bash
#
# Regenerate docs/ from the provider schema and examples/ with
# terraform-plugin-docs.
#
# tfplugindocs builds the provider from the repository root, but the
# entrypoint lives under cmd/, and it only looks the schema up under the bare
# name or the "hashicorp/" namespace. So the script builds the binary itself,
# exports the schema through a throwaway "hashicorp/agentmail" dev override,
# and passes it in with --providers-schema. The namespace is only a lookup
# key; the rendered docs use the source from examples/provider/provider.tf.
#
# Requires go and terraform. Runs from anywhere.

set -euo pipefail

cd "$(dirname "$0")/.."

PROVIDER_NAME=agentmail
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

go build -o "$tmp/terraform-provider-${PROVIDER_NAME}" "./cmd/terraform-provider-${PROVIDER_NAME}"

cat > "$tmp/dev.tfrc" <<TFRC
provider_installation {
  dev_overrides {
    "hashicorp/${PROVIDER_NAME}" = "$tmp"
  }
  direct {}
}
TFRC

mkdir -p "$tmp/cfg"
cat > "$tmp/cfg/main.tf" <<TF
terraform {
  required_providers {
    ${PROVIDER_NAME} = { source = "hashicorp/${PROVIDER_NAME}" }
  }
}
TF

(
  cd "$tmp/cfg"
  TF_CLI_CONFIG_FILE="$tmp/dev.tfrc" terraform providers schema -json > "$tmp/schema.json"
)

go tool tfplugindocs generate \
  --provider-name "${PROVIDER_NAME}" \
  --providers-schema "$tmp/schema.json"
