# Changelog

## 1.1.0 (November 24, 2026)

FEATURES:

* **New Resource:** `omniddi_dns_zone`
* **New Resource:** `omniddi_dns_record`
* **New Resource:** `omniddi_ipam_container`
* **New Resource:** `omniddi_ipam_subnet`
* **New Resource:** `omniddi_dhcp_subnet`
* **New Resource:** `omniddi_dhcp_host_reservation`
* **New Data Sources:** `omniddi_dns_view(s)`, `omniddi_dns_zone(s)`, `omniddi_ipam_container(s)`, `omniddi_ipam_subnet(s)`, `omniddi_ipam_node`, `omniddi_network_space(s)`, `omniddi_server_group(s)`
* Authentication with an OmniDDI account's username and password (`OMNIDDI_USERNAME`, `OMNIDDI_PASSWORD`), with automatic session refresh and the network space selected by `network_space_id`. Accounts with MFA are not supported; use a dedicated automation account. API key authentication is planned for a later release.
