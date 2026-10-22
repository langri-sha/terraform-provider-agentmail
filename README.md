# terraform-provider-agentmail

[![Test](https://github.com/langri-sha/terraform-provider-agentmail/actions/workflows/test.yml/badge.svg)](https://github.com/langri-sha/terraform-provider-agentmail/actions/workflows/test.yml)

A Terraform provider for [AgentMail](https://agentmail.to): manage agent inboxes, allow lists and API keys as code.

It manages inboxes as `agentmail_inbox`, each allow or block list as one
authoritative `agentmail_list`, and mints scoped, expiring API keys with the
ephemeral `agentmail_api_key`, whose secret never reaches plan or state.
Pre-1.0 and not yet published to the Terraform Registry.

## Usage

```hcl
terraform {
  required_providers {
    agentmail = {
      source = "langri-sha/agentmail"
    }
  }
}

# Reads the API key from AGENTMAIL_API_KEY. For the EU, set
# base_url = "https://api.agentmail.eu".
provider "agentmail" {}

resource "agentmail_inbox" "support" {
  username     = "support"
  display_name = "Support"

  lifecycle {
    prevent_destroy = true
  }
}

resource "agentmail_list" "support_allow" {
  for_each = toset(["receive", "reply"])

  inbox_id  = agentmail_inbox.support.inbox_id
  direction = each.key
  list_type = "allow"
  entries   = [{ entry = "example.com" }]
}
```

Replacing or destroying an inbox deletes its messages, threads and drafts.
`username`, `domain`, `pod_id` and `client_id` can't change in place, so
changing one replaces the inbox; such plans carry a warning.

An allow list blocks every address it does not match from its first entry on,
so `agentmail_list` adds entries before it removes any, and destroying an
allow list opens it again.

## API keys

The ephemeral `agentmail_api_key` mints a key scoped to the organization, a
pod or an inbox, with the permissions you list and an expiry. Terraform opens
ephemeral resources during plan as well as apply, so every run mints a key,
and the provider's own key needs `api_key_create`. By default the key is
revoked when the run ends, which suits configuring another provider:

```hcl
ephemeral "agentmail_api_key" "inboxes" {
  permissions = ["inbox_read", "inbox_create", "inbox_update", "inbox_delete"]
  ttl         = "1h"
}

provider "agentmail" {
  alias   = "inboxes"
  api_key = ephemeral.agentmail_api_key.inboxes.api_key
}
```

To store a key that outlives the run, set `revoke_on_close = false` with a
`ttl` or `expires_at`, and hand it to a write-only attribute. See the
[`agentmail_api_key` docs](./docs/ephemeral-resources/api_key.md).

## Importing

```sh
terraform import agentmail_inbox.support support@agentmail.to
terraform import 'agentmail_list.support_allow["receive"]' inbox/support@agentmail.to/receive/allow
```

An imported inbox plans clean against a configuration that sets only what it
manages. Creating an `agentmail_list` over a list that already holds entries
fails and asks for an import, rather than deleting them.

See [`docs/`](./docs) for the full schema.

## Contributing

Bug reports and pull requests are welcome. See [CONTRIBUTING.md](./CONTRIBUTING.md)
for the repository layout, local development, and the release process.

## License

Apache 2.0. See [`LICENSE`](./LICENSE).
