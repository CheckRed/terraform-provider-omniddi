---
page_title: "Approvals"
subcategory: ""
description: |-
  How the provider behaves when OmniDDI requires approval for a change.
---

# Approvals

OmniDDI can require a change to be approved before it is applied. The provider's `approval_mode` controls what happens then. Set it on the provider, or override it per resource with the resource's own `approval_mode`. Resources that support it: `omniddi_dns_zone`, `omniddi_dns_record`, `omniddi_dhcp_subnet` and `omniddi_dhcp_host_reservation`. IPAM containers and subnets are applied synchronously and never wait for approval.

| Mode | Behaviour |
|---|---|
| `fail` (default) | The apply stops with an error that names the approval request id. Nothing is saved to state for a create. |
| `wait` | The provider polls the request until it is applied, then continues. The run holds until someone approves. |

## Recommended pipeline

Human approval can take hours, which can exceed a CI or HCP Terraform run timeout. Use `fail` and a two-step flow:

1. Run `terraform apply`. It fails with `Approval required (request N)`.
2. Approve request N in OmniDDI.
3. Run `terraform apply` again. The provider finds the approved change and completes it.

Use `wait` only where runs can stay open until approval, and raise `timeout` accordingly.

## Duplicate requests

Before it submits a change, the provider looks for a pending request for the same action and target. If one exists, it reports that request (`fail`) or resumes waiting on it (`wait`) instead of filing a second one. Re-running an interrupted apply is therefore safe.

## Zone creation

When a zone create needs approval, OmniDDI may return no request id. The provider cannot track it and fails with an explanation. Approve the pending zone request in OmniDDI, then run `terraform apply` again.

## Rejected requests

If the request is rejected or withdrawn, the provider returns an error and the change is not applied.
