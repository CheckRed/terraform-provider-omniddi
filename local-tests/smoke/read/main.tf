terraform {
  required_providers {
    omniddi = {
      source  = "checkred/omniddi"
      version = ">= 1.0.0"
    }
  }
}

provider "omniddi" {}

data "omniddi_network_spaces" "all" {}

data "omniddi_dns_views" "all" {}

data "omniddi_server_groups" "all" {}

output "network_spaces" {
  description = "Network spaces the account can see."
  value       = [for s in data.omniddi_network_spaces.all.network_spaces : "${s.id}: ${s.name} (${s.state})"]
}

output "dns_views" {
  description = "DNS views in the current network space."
  value       = [for v in data.omniddi_dns_views.all.views : v.name]
}

output "server_groups" {
  description = "Server groups in the current network space."
  value       = [for g in data.omniddi_server_groups.all.server_groups : "${g.name} (${g.type})"]
}
