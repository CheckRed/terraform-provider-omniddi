data "omniddi_server_groups" "dhcp" {
  type = "dhcp"
}

output "dhcp_group_names" {
  value = [for g in data.omniddi_server_groups.dhcp.server_groups : g.name]
}
