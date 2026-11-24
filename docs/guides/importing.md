---
page_title: "Importing existing objects"
subcategory: ""
description: |-
  Import ids and natural keys for each resource.
---

# Importing existing objects

Every resource imports by its numeric OmniDDI `id`. Most also accept a natural key, which the provider resolves to the id. State always stores the id.

```shell
terraform import omniddi_dns_zone.corp 34
terraform import omniddi_dns_zone.corp internal/corp.example.com
```

Imports set `deletion_protection = true` and `force_destroy = false`. Terraform 1.12 and later can also import with an `import` block and the `id` identity.

| Resource | Natural key |
|---|---|
| `omniddi_dns_zone` | `<view name>/<zone name>` |
| `omniddi_dns_record` | `<view name>/<zone name>/<record name>/<type>`, with `@` for the zone apex |
| `omniddi_ipam_container` | `<cidr>`, for example `10.0.0.0/8` |
| `omniddi_ipam_subnet` | `<cidr>`, for example `10.10.1.0/24` |
| `omniddi_dhcp_subnet` | `<subnet cidr>`, for example `10.20.0.0/24` |
| `omniddi_dhcp_host_reservation` | `<subnet cidr>/<reservation name>` |

Each resource page has the exact command. Write the matching configuration first, then run `terraform plan` and check that it shows no changes.
