---
page_title: "Deletion protection"
subcategory: ""
description: |-
  How deletion_protection and force_destroy guard destructive deletes.
---

# Deletion protection

Deleting some OmniDDI objects also deletes what is inside them. Two attributes guard against accidents.

- `deletion_protection` defaults to `true`. While it is `true`, destroy fails.
- `force_destroy` defaults to `false`. While it is `false`, destroy fails if the object still holds dependent objects.

To delete a protected object, set `deletion_protection = false`, run `terraform apply`, then destroy it. Both attributes are stored in state, so the change must be applied before the destroy.

| Resource | `deletion_protection` | `force_destroy` |
|---|---|---|
| `omniddi_dns_zone` | yes | Allow deleting while the zone has records that were not created automatically. OmniDDI deletes all records with the zone. |
| `omniddi_ipam_container` | yes | Skip the provider check that refuses to delete a container with child networks. OmniDDI still refuses unless `reparent_children_on_destroy = true`, which moves the children to the parent. Children are never deleted. |
| `omniddi_ipam_subnet` | yes | Allow deleting a subnet that has DHCP service configured. Its DHCP configuration, reservations and leases are removed with it. |
| `omniddi_dhcp_subnet` | yes | Allow demoting a subnet that still has host reservations or active leases. Destroying it only stops serving DHCP: reservations stay on the IPAM subnet and leases remain until they expire, but clients lose service. |

```terraform
resource "omniddi_dns_zone" "example" {
  view_id = 12
  name    = "example.com"

  deletion_protection = false
  force_destroy       = true
}
```

Imported objects get the defaults (`deletion_protection = true`, `force_destroy = false`).

Setting `deletion_protection = false` in version control is a deliberate act. Review such changes carefully. A destroy of a zone can remove many records at once.
