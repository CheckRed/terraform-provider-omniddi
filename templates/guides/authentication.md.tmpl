---
page_title: "Authentication"
subcategory: ""
description: |-
  How the provider signs in to OmniDDI with a username and password, which account to use, and how to pick a network space.
---

# Authentication

The provider signs in to OmniDDI with the **username and password** of an OmniDDI account. It signs in once per run, keeps the session alive while Terraform works, and signs in again if the session ends.

```terraform
provider "omniddi" {
  endpoint = "https://omniddi.example.com"
  username = "terraform-automation@example.com"
  # password comes from OMNIDDI_PASSWORD
}
```

Set the password through the `OMNIDDI_PASSWORD` environment variable, filled from your CI secret store or a vault. Do not write it in `.tf` files or `.tfvars` files, and never commit it. The provider masks `password` and `client_key_pem` in logs and redacts tokens from backend messages.

The username is the account's email address, or its LDAP user name when OmniDDI signs users in through LDAP. Accounts that can only sign in through SSO cannot be used.

## Which account to use

Every call runs with the roles and permissions of the account Terraform signs in as, and the audit log records that account as the actor.

Create a **dedicated automation account** for Terraform. Do not use a person's account. Give it a role that can:

- read and write the objects you manage (DNS zones and records, IPAM containers and subnets, DHCP subnets and host reservations);
- manage tags on those objects if you use tags;
- `read_servers`, which the provider needs to poll the status of the agent tasks that apply DNS changes.

The account must:

- **not have MFA enabled.** Terraform cannot answer an MFA challenge, so sign-in fails with a clear error. If your MFA policy covers every account, exempt this one account or role.
- **have finished onboarding**, including any forced password change. Sign in once in the OmniDDI web interface to complete it.

Because the automation account has no MFA, protect it in other ways:

- use a long, random password kept in a secret manager, and rotate it on a schedule;
- give it only the roles it needs;
- review its activity in the OmniDDI audit log.

## Password changes and sessions

Changing the account's password ends its sessions. Update the CI secret at the same time; the next run signs in with the new password. A run that is already in progress may fail with an authentication error; run it again.

Sign-in is rate-limited per IP address, 10 sign-ins per minute by default. The provider retries when it is limited, but many Terraform runs starting at once from one host can still fail. Stagger them or run them from different hosts.

## Network space

Every object lives in one network space, and one provider configuration works in one space. After signing in, the provider switches to the space set in `network_space_id`. Without it, the provider works in the account's landing space, normally the Default space. The account must have access to the space, or sign-in fails with HTTP 403.

To manage several spaces, declare one provider block per space with an `alias`:

```terraform
provider "omniddi" {
  alias            = "lab"
  endpoint         = "https://omniddi.example.com"
  username         = "terraform-automation@example.com"
  network_space_id = 2
}
```

Resources then select it with `provider = omniddi.lab`.

Do not change `network_space_id` on a provider that already manages objects. Objects are looked up by id within the configured space, so after the change Terraform no longer finds them, drops them from state and plans to create them again in the new space. Move a workload to another space with a separate provider alias and a new set of resources.

To find a space id, use the `omniddi_network_space` data source on a provider configured for a space the account can use (for example the Default space, with `network_space_id` unset), or read the id in OmniDDI. A provider block cannot read a data source of its own configuration.
