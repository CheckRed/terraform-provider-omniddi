# Terraform Provider for OmniDDI

The OmniDDI provider lets you manage OmniDDI — DNS, DHCP and IP address management (DDI) — as code with Terraform.

## What you can manage

| Area | Resources | Data sources |
|---|---|---|
| DNS | `omniddi_dns_zone`, `omniddi_dns_record` | `omniddi_dns_view(s)`, `omniddi_dns_zone(s)` |
| IPAM | `omniddi_ipam_container`, `omniddi_ipam_subnet`, `omniddi_ipam_address` | `omniddi_ipam_container(s)`, `omniddi_ipam_subnet(s)`, `omniddi_ipam_node` |
| DHCP | `omniddi_dhcp_subnet`, `omniddi_dhcp_host_reservation` | — |
| Platform | — | `omniddi_network_space(s)`, `omniddi_server_group(s)` |

## Requirements

- Terraform 1.12 or later
- An OmniDDI management server reachable over HTTPS
- A dedicated OmniDDI automation account without MFA

## Quick start

```terraform
terraform {
  required_providers {
    omniddi = {
      source  = "checkred/omniddi"
      version = "~> 1.1"
    }
  }
}

provider "omniddi" {
  endpoint = "https://omniddi.example.com"
  username = "terraform-automation@example.com"
}

data "omniddi_dns_view" "internal" {
  name = "internal"
}

resource "omniddi_dns_zone" "corp" {
  view_id = data.omniddi_dns_view.internal.id
  name    = "corp.example.com"
}

resource "omniddi_dns_record" "www" {
  zone_id = omniddi_dns_zone.corp.id
  name    = "www"
  type    = "A"
  values  = ["192.0.2.10"]
}
```

Pass the password through the environment, never in `.tf` files:

```bash
export OMNIDDI_PASSWORD="..."
```

```bash
terraform init && terraform plan
```

Every provider setting can also be set through an `OMNIDDI_*` environment variable, for example `OMNIDDI_ENDPOINT`, `OMNIDDI_USERNAME` and `OMNIDDI_NETWORK_SPACE_ID`.

## Documentation

- [Provider configuration](docs/index.md)
- [Resources](docs/resources) and [data sources](docs/data-sources)
- Guides: [authentication](docs/guides/authentication.md), [approvals](docs/guides/approvals.md), [asynchronous writes and troubleshooting](docs/guides/async-writes-and-troubleshooting.md), [deletion protection](docs/guides/deletion-protection.md), [tags](docs/guides/tags.md), [importing existing objects](docs/guides/importing.md)
- Runnable examples: [examples/](examples)

## Building from source

Requires Go (version in [src/go.mod](src/go.mod)) and GNU Make.

```bash
make build
```

```bash
make test
```

To try a local build, point Terraform at it with a `dev_overrides` block in `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "checkred/omniddi" = "/path/to/terraform-provider-omniddi/src"
  }
  direct {}
}
```

Acceptance tests run against a real OmniDDI server and create objects there:

```bash
OMNIDDI_ENDPOINT=... OMNIDDI_USERNAME=... OMNIDDI_PASSWORD=... make testacc
```

## Contributing

Issues and pull requests are welcome. Use [Conventional Commits](https://www.conventionalcommits.org/) for PR titles, sign your commits, and run `make test lint docs-check` before opening a PR. Changes are listed in [CHANGELOG.md](CHANGELOG.md).

## Security

Please do not report security vulnerabilities in public issues. Use GitHub's **Report a vulnerability** button on the repository's Security tab instead.

## License

Proprietary. Copyright (c) 2026 Akamai-DDI. All rights reserved. Use of this provider is governed by the [Akamai-DDI Proprietary License](LICENSE).
