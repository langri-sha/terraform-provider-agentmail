# Agents orientation — `langri-sha/terraform-provider-agentmail`

A Terraform provider, written in Go using the
[Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework),
for [AgentMail](https://agentmail.to). See [`README.md`](./README.md) for the
user-facing description and [`CONTRIBUTING.md`](./CONTRIBUTING.md) for the
layout, the development commands and the checks every change must pass.

## Upstream surface

Source of truth is AgentMail's OpenAPI document, vendored at
`internal/fakeagentmail/testdata/openapi.yml` from the commit named in
`openapi.source`. `scripts/update-openapi.sh [sha]` bumps it; review the diff,
then make the contract tests pass again. Do not use the official
`agentmail-go`: it is generated from an older spec and lacks key expiry and
other current fields.

Bearer auth (`Authorization: Bearer <key>`) against `https://api.agentmail.to`,
or `https://api.agentmail.eu` in the EU. Errors carry
`{name, code, message, fix}`; branch on `code`, never on `message`. A 429
carries `Retry-After`. Lists page with `limit` and `page_token` until
`next_page_token` is absent; a page can be empty and still carry one.

The operations the provider uses:

- **Inboxes.** `POST /v0/inboxes` or `POST /v0/pods/{pod_id}/inboxes`, then
  `GET`, `PATCH` and `DELETE /v0/inboxes/{inbox_id}`.
  - `inbox_id` is the email address. Responses carry `email` but never
    `username` or `domain`.
  - `username`, `domain`, `client_id` and the pod are create-only.
    `display_name`, `status` (`active` or `paused`) and `metadata` are
    patchable.
  - `status` is omitted while the inbox is active.
  - `client_id` makes a create idempotent: a repeat returns the original
    inbox.
  - `PATCH` must set at least one field.
  - `metadata` is a merge patch. Included keys are set, a key sent as `null`
    is removed, and keys left out are kept. `metadata: null` clears everything;
    an empty object is rejected. Values are strings, numbers or booleans; up to
    256 keys of at most 256 characters, and string values of at most 256.
  - Deleting an inbox deletes its messages, threads and drafts.
- **Allow and block lists.** `GET` and `POST .../lists/{direction}/{type}`, and
  `GET` and `DELETE .../lists/{direction}/{type}/{entry}`, under `/v0` (the
  organization), `/v0/pods/{pod_id}` or `/v0/inboxes/{inbox_id}`.
  - `direction` is `send`, `receive` or `reply`; `type` is `allow` or `block`.
  - An entry is an email address or a domain, with an optional `reason`.
    Entries have no update.
  - An allow list blocks every address it does not match from its first entry
    on.
  - Reply lists replace receive lists for replies.
  - The most specific scope with a match wins: inbox, then pod, then
    organization.
  - `read_only` entries can't be deleted through the API.
- **API keys.** `POST /v0/api-keys`, `/v0/pods/{pod_id}/api-keys` or
  `/v0/inboxes/{inbox_id}/api-keys`; `GET /v0/api-keys/{api_key_id}`; `DELETE`
  through the creating scope's route.
  - The secret, `api_key`, is returned only by the create.
  - `permissions` omitted grants everything; otherwise only the permissions
    set to `true` are granted, and a restricted key can't grant more than it
    holds.
  - `expires_at` is immutable and must be in the future. Without it the key
    inherits the creating key's expiry. A later `expires_at` than the creating
    key's own returns 403. Requests with an expired key are rejected within
    about five minutes.

Don't invent fields. If the document doesn't define it, leave it out.

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

## Tests

`internal/fakeagentmail` is an in-memory AgentMail. Its contract test runs the
client through every operation and checks each exchange against the vendored
document: the route, the status, and both bodies, with object fields closed.
A status the document leaves undeclared needs an entry in
`undeclaredStatuses`, citing the docs or spec text that justifies it. Teach the
fake new behaviour from the document or the docs, never from guesses.

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
