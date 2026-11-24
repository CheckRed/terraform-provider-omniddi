data "omniddi_ipam_subnet" "office" {
  cidr = "10.20.0.0/24"
}

data "omniddi_server_group" "dhcp" {
  name = "edge-dhcp"
  type = "dhcp"
}

resource "omniddi_dhcp_subnet" "office" {
  subnet_id       = data.omniddi_ipam_subnet.office.id
  server_group_id = data.omniddi_server_group.dhcp.id

  lease_time      = 8
  lease_time_unit = "hrs"

  option_data = [
    { code = 3, data = "10.20.0.1" },
    { code = 6, data = "10.20.0.2,10.20.0.3" },
    { code = 15, data = "example.test" },
  ]

  pools = [
    {
      pool = "10.20.0.100-10.20.0.200"
      name = "dynamic"
    },
  ]

  approval_mode       = "wait"
  deletion_protection = true
}
