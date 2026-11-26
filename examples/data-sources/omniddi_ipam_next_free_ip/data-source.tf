check "app_subnet_capacity" {
  data "omniddi_ipam_next_free_ip" "app" {
    subnet_cidr = "10.10.1.0/24"
    quantity    = 10
  }

  assert {
    condition     = length(data.omniddi_ipam_next_free_ip.app.addresses) == 10
    error_message = "Subnet 10.10.1.0/24 has fewer than 10 free addresses."
  }
}
