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

# A key for CI that outlives the run, stored through a write-only attribute
# so it never reaches state. Every plan and apply would mint a new key, so it
# is minted only when rotate_ci_key is true, and it expires on its own. A
# rotation's plan mints a key too, which stays valid until it expires; keep
# ttl short enough for that.
variable "rotate_ci_key" {
  type    = bool
  default = false
}

variable "ci_key_version" {
  type = number
}

ephemeral "agentmail_api_key" "ci" {
  count = var.rotate_ci_key ? 1 : 0

  name            = "ci"
  permissions     = ["inbox_read", "message_send"]
  ttl             = "2160h"
  revoke_on_close = false
}

resource "aws_secretsmanager_secret_version" "ci_agentmail_key" {
  secret_id                = "ci/agentmail-api-key"
  secret_string_wo         = var.rotate_ci_key ? ephemeral.agentmail_api_key.ci[0].api_key : null
  secret_string_wo_version = var.ci_key_version
}
