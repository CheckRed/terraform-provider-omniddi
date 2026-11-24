data "omniddi_dns_zones" "all" {}

data "omniddi_dns_zones" "internal_forward" {
  view_id = 12

  filters = [
    { field = "zoneType", operator = "EQ", value = "Forward" },
  ]
}
