---
page_title: "Asynchronous writes and troubleshooting"
subcategory: ""
description: |-
  How DNS writes converge, what happens on failure, and how to diagnose problems.
---

# Asynchronous writes and troubleshooting

## How writes converge

DNS changes are accepted by OmniDDI and then applied to servers by agent tasks. After a create or update, the provider polls those tasks until they complete. IPAM changes and some others are synchronous.

The provider saves the object to Terraform state before it waits. If the wait fails, the object is not lost and a retry does not create a duplicate.

## When a task fails

- **Create:** the object stays in state and Terraform marks the resource tainted. The next apply replaces it.
- **Update:** the new values are saved, and the next plan shows the change again.
- **Delete:** the object stays in state.

The error includes the failed task id, its correlation id and the task logs.

## New zones and inactive records

Creating a zone starts an automatic first sync, and the provider waits for it before Terraform creates the zone's records. If that wait fails or times out, the zone is still created and the provider warns `Zone created; its first sync did not finish`. That sync can then retire, or mark inactive, records created while it is still running.

The provider recovers on its own:

- When a record it just wrote reads back as inactive, the provider reactivates it in the same apply.
- When an inactive record is found on refresh, the plan shows it as a create. On apply, the provider reactivates that record and applies your configuration to it, instead of failing with `An inactive ... record already exists`. It reports the warning `Reactivated an inactive DNS record`.
- Reactivation takes two writes: one reactivates the record, the next applies your configuration. If the second write fails or needs approval, the provider still saves the record in state and fails with `DNS record was reactivated but this configuration was not applied to it`. The record is marked tainted, so the next apply replaces it with your configuration.
- If the reactivation itself needs approval, the apply stops with the request id. Once it is approved, the next apply finds the approved reactivation, applies your configuration to that record and saves it, instead of failing with `already exists`.
- A CNAME blocked by an inactive record of another type is not reactivated. Remove or reactivate the other record in OmniDDI first.

If a record was retired after the apply finished, run `terraform apply` again.

## Timeouts

Waiting is bounded by the provider `timeout` (default `10m`). Override it per operation with the `timeouts` block on a resource:

```terraform
resource "omniddi_dns_zone" "example" {
  # ...
  timeouts = {
    create = "20m"
  }
}
```

On timeout the provider stops waiting but the change may still complete in OmniDDI. The error shows the correlation id (or approval request id). Check Agent Tasks in OmniDDI, then run `terraform apply` again. Ctrl-C behaves the same way: state is already saved, and the next run reconciles.

## Retries

Read-only calls are retried on transient failures, up to `max_retries` (default 3). Writes are never retried.

## Diagnostics

Every API call sends a fresh `X-Request-ID` header, and errors from the API include it. Quote it, with the correlation id when present, to OmniDDI support. Errors also carry the backend message verbatim.

For a full trace, set `TF_LOG=DEBUG`. Secrets are masked in provider logs.

```shell
TF_LOG=DEBUG terraform apply 2> apply.log
```

## Common errors

| Message | Cause and action |
|---|---|
| `Approval required (request N)` | See the [approvals guide](approvals.md). |
| `agent task N ... failed` | Read the task logs in the error and in OmniDDI Agent Tasks. Fix the cause and apply again. |
| `no agent tasks found for correlation id` | The change was accepted but cannot be confirmed. Check the service account has `read_servers`. |
| `Zone created; its first sync did not finish` | The zone is fine. Records in the same apply are reactivated if the sync retires them; see [New zones and inactive records](#new-zones-and-inactive-records). |
| `Authentication failed` | Check `username` and `OMNIDDI_PASSWORD`, and that the account is active, has no MFA and has finished onboarding. See the [authentication guide](authentication.md). |
| `Deletion protection is enabled` | See the [deletion protection guide](deletion-protection.md). |
| `tag definition(s) not found` | See the [tags guide](tags.md). |
