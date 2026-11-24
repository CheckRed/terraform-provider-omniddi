data "omniddi_ipam_node" "ipv4_root" {
  protocol = "IPv4"
}

data "omniddi_ipam_node" "corp" {
  cidr = "10.0.0.0/8"
  type = "CONTAINER"
}
