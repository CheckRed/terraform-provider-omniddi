data "omniddi_dns_zone" "corp" {
  name      = "corp.example.com"
  view_name = "internal"
}

resource "omniddi_dns_record" "www" {
  zone_id = data.omniddi_dns_zone.corp.id
  name    = "www"
  type    = "A"
  ttl     = 300
  values  = ["192.0.2.10", "192.0.2.11"]
}

resource "omniddi_dns_record" "www_v6" {
  zone_id = data.omniddi_dns_zone.corp.id
  name    = "www"
  type    = "AAAA"
  values  = ["2001:db8::10"]
}

resource "omniddi_dns_record" "delegation" {
  zone_id = data.omniddi_dns_zone.corp.id
  name    = "lab"
  type    = "NS"
  values  = ["ns1.lab.corp.example.com.", "ns2.lab.corp.example.com."]
}

resource "omniddi_dns_record" "sip" {
  zone_id = data.omniddi_dns_zone.corp.id
  name    = "_sip._tcp"
  type    = "SRV"

  srv {
    priority = 10
    weight   = 60
    port     = 5060
    host     = "sip1.corp.example.com"
  }

  srv {
    priority = 10
    weight   = 40
    port     = 5060
    host     = "sip2.corp.example.com"
  }
}

data "omniddi_dns_zone" "reverse" {
  name      = "2.0.192.in-addr.arpa"
  view_name = "internal"
}

resource "omniddi_dns_record" "www_ptr" {
  zone_id = data.omniddi_dns_zone.reverse.id
  name    = "10"
  type    = "PTR"
  values  = ["www.corp.example.com."]
}

resource "omniddi_dns_record" "apex_mx" {
  zone_id = data.omniddi_dns_zone.corp.id
  name    = "@"
  type    = "MX"

  mx {
    preference = 10
    exchange   = "mail1.example.com"
  }

  mx {
    preference = 20
    exchange   = "mail2.example.com"
  }
}

resource "omniddi_dns_record" "apex_caa" {
  zone_id = data.omniddi_dns_zone.corp.id
  name    = "@"
  type    = "CAA"

  caa {
    flag  = 0
    tag   = "issue"
    value = "letsencrypt.org"
  }

  caa {
    flag  = 0
    tag   = "iodef"
    value = "mailto:security@example.com"
  }
}

resource "omniddi_dns_record" "verification" {
  zone_id         = data.omniddi_dns_zone.corp.id
  name            = "_verify"
  type            = "TXT"
  values          = ["token-current", "token-retiring"]
  disabled_values = ["token-retiring"]
}

resource "omniddi_dns_record" "service_binding" {
  zone_id = data.omniddi_dns_zone.corp.id
  name    = "svc"
  type    = "SVCB"

  data_json = jsonencode({
    priority = 1
    host     = "svc.example.com"
    alpn     = "h2"
    port     = 8443
  })
}
