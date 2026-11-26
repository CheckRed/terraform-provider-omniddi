data "omniddi_ipam_container" "datacenter" {
  cidr = "10.10.0.0/16"
}

resource "omniddi_ipam_subnet" "lab" {
  parent_id    = data.omniddi_ipam_container.datacenter.id
  network_addr = "10.10.1.0"
  prefix       = 24
  name         = "lab"
  description  = "Lab servers"
  owner        = "netops"
  vlan_id      = 110

  deletion_protection = true

  tags = {
    env = "prod"
  }
}

resource "omniddi_ipam_subnet" "documentation_v6" {
  network_addr = "2001:db8:1::"
  prefix       = 48
}

resource "omniddi_ipam_subnet" "app" {
  parent_id = data.omniddi_ipam_container.datacenter.id
  prefix    = 26
  name      = "app"
  exclude   = ["10.10.0.0/24"]
}
