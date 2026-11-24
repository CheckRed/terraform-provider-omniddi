---
page_title: "Tags"
subcategory: ""
description: |-
  How OmniDDI tags work with the provider.
---

# Tags

Taggable resources: `omniddi_dns_zone`, `omniddi_dns_record`, `omniddi_ipam_container`, `omniddi_ipam_subnet` and `omniddi_dhcp_host_reservation`.

## Definitions must exist

Tag definitions are created in OmniDDI, not by this provider. If a tag name is not defined for the object type, the apply fails with `tag definition(s) not found`. The object itself is already saved in state at that point, so fix the definition and apply again.

## Attributes

| Attribute | Type | Purpose |
|---|---|---|
| `tags` | map of string | Single-value tags that you manage. |
| `tags_list` | map of list of string | Tags whose definition allows several values. |
| `tags_all` | map of string (computed) | Single-value tags on the object: yours plus provider `default_tags`. |

A name cannot appear in both `tags` and `tags_list`. Tags inherited from a parent object are ignored.

## Default tags

`default_tags` on the provider are applied to every taggable resource. A resource's own `tags` win when the name is the same.

```terraform
provider "omniddi" {
  endpoint = "https://omniddi.example.com"
  username = "terraform-automation@example.com"

  default_tags = {
    "Managed-By" = "terraform"
  }
}

data "omniddi_dns_view" "internal" {
  name = "internal"
}

resource "omniddi_dns_zone" "corp" {
  view_id = data.omniddi_dns_view.internal.id
  name    = "corp.example.com"

  tags = {
    env = "prod"
  }
}
```

The default tags need definitions too. Tags are written after the object is saved. A tag failure returns an error, and the object is still in state.
