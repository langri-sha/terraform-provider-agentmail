# Examples

Layout follows [terraform-plugin-docs](https://github.com/hashicorp/terraform-plugin-docs)
conventions: `scripts/generate-docs.sh` renders these files into `docs/`.

```
provider/provider.tf                                    -> docs/index.md
resources/<name>/resource.tf, import.sh                 -> docs/resources/<name>.md
ephemeral-resources/<name>/ephemeral-resource.tf        -> docs/ephemeral-resources/<name>.md
```

## Running against a local build

To try unreleased changes, build the binary and point a `~/.terraformrc` at it
with `dev_overrides`:

```hcl
provider_installation {
  dev_overrides {
    "langri-sha/agentmail" = "/path/to/terraform-provider-agentmail/dist"
  }
  direct {}
}
```

Then `go build -o dist/terraform-provider-agentmail ./cmd/terraform-provider-agentmail`
and run `terraform plan` from any example directory.
