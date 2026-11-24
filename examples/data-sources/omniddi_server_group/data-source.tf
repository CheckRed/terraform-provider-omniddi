data "omniddi_server_group" "by_name" {
  name = "edge-authoritative"
  type = "dns"
}

data "omniddi_server_group" "by_id" {
  id = 12
}
