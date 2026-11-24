data "omniddi_dns_view" "internal" {
  name = "internal"
}

resource "omniddi_dns_zone" "corp" {
  view_id = data.omniddi_dns_view.internal.id
  name    = "corp.example.com"

  ttl     = 3600
  refresh = 900
  email   = "hostmaster@example.com"

  enable_zone_transfer = true

  deletion_protection = false

  tags = {
    env = "prod"
  }
}

resource "omniddi_dns_zone" "reverse" {
  view_id   = data.omniddi_dns_view.internal.id
  zone_type = "Reverse"
  protocol  = "IPv4"
  cidr      = "192.0.2.0/24"

  deletion_protection = false
}

resource "omniddi_dns_zone" "mirror" {
  view_id   = data.omniddi_dns_view.internal.id
  name      = "mirror.example.com"
  zone_type = "Secondary"

  primary_servers = [
    { ip = "198.51.100.10" },
  ]

  deletion_protection = false
}

resource "omniddi_dns_zone" "forward" {
  view_id     = data.omniddi_dns_view.internal.id
  name        = "partner.example.net"
  zone_type   = "Forward"
  nameservers = ["198.51.100.53"]

  deletion_protection = false
}
