terraform {
  required_providers {
    agentmail = {
      source = "langri-sha/agentmail"
    }
  }
}

# Reads the API key from AGENTMAIL_API_KEY.
provider "agentmail" {}

# Or pass it in, for example from a secret store, and use the EU API.
provider "agentmail" {
  alias    = "eu"
  api_key  = var.agentmail_api_key
  base_url = "https://api.agentmail.eu"
}

variable "agentmail_api_key" {
  type      = string
  sensitive = true
  ephemeral = true
}
