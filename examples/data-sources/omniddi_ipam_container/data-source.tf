data "omniddi_ipam_container" "by_cidr" {
  cidr = "10.0.0.0/8"
}

data "omniddi_ipam_container" "by_id" {
  id = 42
}
