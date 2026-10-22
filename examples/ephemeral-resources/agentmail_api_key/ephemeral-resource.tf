# A short-lived key that can only manage inboxes, handed to a second provider
# configuration. It is revoked when the run ends.
ephemeral "agentmail_api_key" "inboxes" {
  name        = "terraform-inboxes"
  permissions = ["inbox_read", "inbox_create", "inbox_update", "inbox_delete"]
  ttl         = "1h"
}

provider "agentmail" {
  alias   = "inboxes"
  api_key = ephemeral.agentmail_api_key.inboxes.api_key
}

resource "agentmail_inbox" "support" {
  provider = agentmail.inboxes
  username = "support"
}
