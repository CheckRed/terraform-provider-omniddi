data "omniddi_ipam_subnet" "app" {
  cidr = "10.10.1.0/24"
}

data "omniddi_dns_zone" "corp" {
  name      = "corp.example.com"
  view_name = "internal"
}

resource "omniddi_ipam_address" "web" {
  subnet_id = data.omniddi_ipam_subnet.app.id
  name      = "web-01.corp.example.com"
  exclude   = ["10.10.1.1"]
}

resource "omniddi_dns_record" "web" {
  zone_id = data.omniddi_dns_zone.corp.id
  name    = "web-01"
  type    = "A"
  values  = [omniddi_ipam_address.web.ip_address]
}
