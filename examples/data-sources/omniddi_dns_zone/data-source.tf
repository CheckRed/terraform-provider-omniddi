data "omniddi_dns_zone" "by_id" {
  id = 34
}

data "omniddi_dns_zone" "by_name" {
  name      = "corp.example.com"
  view_name = "internal"
}

data "omniddi_dns_zone" "by_view_id" {
  name    = "corp.example.com"
  view_id = 12
}
