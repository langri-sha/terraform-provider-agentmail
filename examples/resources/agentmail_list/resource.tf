# Accept mail and replies only from known senders. The same entries close
# both lists, because replies are checked against the reply list instead of
# the receive list.
locals {
  partners = [
    { entry = "example.com", reason = "partner" },
    { entry = "alice@example.org" },
  ]
}

resource "agentmail_list" "support_allow" {
  for_each = toset(["receive", "reply"])

  inbox_id  = agentmail_inbox.support.inbox_id
  direction = each.key
  list_type = "allow"
  entries   = local.partners
}

# Block sending to a domain from every inbox in the organization.
resource "agentmail_list" "no_competitors" {
  direction = "send"
  list_type = "block"
  entries   = [{ entry = "competitor.example", reason = "policy" }]
}
