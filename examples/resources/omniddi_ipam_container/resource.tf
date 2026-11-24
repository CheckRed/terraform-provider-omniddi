data "omniddi_ipam_node" "ipv4_root" {
  protocol = "IPv4"
}

resource "omniddi_ipam_container" "corp" {
  parent_id   = data.omniddi_ipam_node.ipv4_root.id
  name        = "corp"
  cidr        = "10.0.0.0/8"
  description = "Corporate address space"
  owner       = "netops"

  deletion_protection = true

  tags = {
    env = "prod"
  }
}

resource "omniddi_ipam_container" "datacenter" {
  parent_id  = omniddi_ipam_container.corp.id
  name       = "datacenter"
  cidr       = "10.10.0.0/16"
  depends_on = [omniddi_ipam_container.corp]

  reparent_children_on_destroy = true
}
