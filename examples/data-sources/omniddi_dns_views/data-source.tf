data "omniddi_dns_views" "all" {}

data "omniddi_dns_views" "high_priority" {
  filters = [
    { field = "priority", operator = "LTE", value = "50" },
  ]
}
