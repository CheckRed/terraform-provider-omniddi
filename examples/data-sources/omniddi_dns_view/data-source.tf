data "omniddi_dns_view" "by_name" {
  name = "internal"
}

data "omniddi_dns_view" "by_id" {
  id = 12
}
