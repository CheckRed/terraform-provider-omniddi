resource "omniddi_dhcp_host_reservation" "printer" {
  name            = "printer-01"
  ip_address      = "10.20.0.50"
  identifier_type = "Mac Address"
  identifier      = "aa:bb:cc:dd:ee:01"
  description     = "Front desk printer"
  approval_mode   = "wait"

  options = [
    { code = 15, data = "example.test" },
  ]

  tags = {
    owner = "netops"
  }
}

resource "omniddi_dhcp_host_reservation" "server_v6" {
  name            = "app-server-v6"
  ip_address      = "2001:db8:20::50"
  identifier_type = "DUID"
  identifier      = "00:01:00:01:2c:4d:5e:6f:aa:bb:cc:dd:ee:02"
  description     = "DHCPv6 reservation bound to the client DUID"
}

resource "omniddi_dhcp_host_reservation" "ip_only" {
  name       = "reserved-gateway"
  ip_address = "10.20.0.1"
}
