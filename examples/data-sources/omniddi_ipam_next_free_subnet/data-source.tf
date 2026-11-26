check "datacenter_capacity" {
  data "omniddi_ipam_next_free_subnet" "next_24" {
    container_cidr = "10.10.0.0/16"
    prefix_length  = 24
  }

  assert {
    condition     = data.omniddi_ipam_next_free_subnet.next_24.cidr != null
    error_message = "Container 10.10.0.0/16 has no free /24 left."
  }
}
