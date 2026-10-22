# Agents orientation — `langri-sha/terraform-provider-agentmail`

A Terraform provider, written in Go using the
[Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework),
for [AgentMail](https://agentmail.to). See [`README.md`](./README.md) for the
user-facing description and [`CONTRIBUTING.md`](./CONTRIBUTING.md) for the
layout, the development commands and the checks every change must pass.

## Conventions

- **No idempotent fluff.** No `cmd || true`, no swallowed errors, no
  "tolerate pre-existing state" patterns. Strict failure with a clear
  error beats silent recovery.
- **Don't speculate, verify.** Before claiming "AgentMail supports X", read
  AgentMail's OpenAPI document or its docs repository. Web-fetched marketing
  pages are not authoritative.
- **HCL field names mirror upstream JSON.** Keep HCL and the API's JSON
  one-to-one wherever possible so users can cross-reference AgentMail's docs
  without translation.

## CI

`test.yml` fails when `go mod tidy` changes `go.mod` or `go.sum`, on
`go vet`, `go test -race` or the build, when
`terraform fmt -check -recursive examples` finds unformatted HCL, and when
`docs/` differs from what `scripts/generate-docs.sh` renders. After changing
the schema or the examples, run the script and commit `docs/`.

## Release

Publishing a GitHub Release for a `vX.Y.Z` tag releases the provider: the
Release workflow builds and signs it with GoReleaser, and the public Terraform
Registry ingests it as `langri-sha/agentmail`. Don't cut a release, push a tag
or touch the signing secrets unless asked to.
