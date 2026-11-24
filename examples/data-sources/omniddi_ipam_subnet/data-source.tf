data "omniddi_ipam_subnet" "by_cidr" {
  cidr = "198.51.100.0/24"
}

data "omniddi_ipam_subnet" "by_id" {
  id = 57
}
