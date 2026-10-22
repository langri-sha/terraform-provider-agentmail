resource "agentmail_inbox" "support" {
  username     = "support"
  display_name = "Support"

  metadata = {
    team = "support"
    tier = 2
  }

  # Replacing or destroying an inbox deletes its mail.
  lifecycle {
    prevent_destroy = true
  }
}

# An inbox on a verified domain, created paused.
resource "agentmail_inbox" "billing" {
  username = "billing"
  domain   = "example.com"
  status   = "paused"
}
