# Local tests

Test the OmniDDI provider against a real OmniDDI server **without publishing it to the Terraform Registry**.

Terraform normally downloads `checkred/omniddi` from the Registry. These scripts point Terraform somewhere else instead:

| Mode | Script | What Terraform uses | Use it to |
|---|---|---|---|
| **Release** | `scripts/install-release.sh [latest\|pre\|version]` | A zip from a GitHub release, after its signature and checksum are checked, served from a local mirror | Test a release exactly as users will get it, without the Registry |
| **Source** | `scripts/install-local-build.sh` | A build of this checkout, through `dev_overrides` | Try code changes before they are released |

Then `scripts/smoke.sh read` or `scripts/smoke.sh write` runs a small Terraform configuration against your server.

## Quick start: test the latest GitHub release

Set the connection (see step 2), then:

```bash
local-tests/scripts/test-release.sh
```

It downloads the latest release from GitHub, checks it, installs it locally and runs the read smoke test, plus the write smoke test when `SMOKE_DNS_VIEW` is set. Pass `pre` for the newest pre-release or a version such as `1.1.0`:

```bash
local-tests/scripts/test-release.sh pre
```

## Requirements

- Terraform 1.12 or later
- For release mode: [GitHub CLI](https://cli.github.com) signed in with access to the repository, and GnuPG (`brew install gnupg`)
- For source mode: Go and GNU Make
- An OmniDDI server and a test automation account without MFA

## 1. Install the provider

Test a GitHub release:

| Argument | Installs |
|---|---|
| none or `latest` | The release marked **Latest** on GitHub |
| `pre` | The newest release, including pre-releases, for example one waiting to be promoted |
| `1.1.0` or `v1.1.0` | That exact version |

```bash
local-tests/scripts/install-release.sh
```

Or build from this checkout:

```bash
local-tests/scripts/install-local-build.sh
```

Run either again to switch mode. The last one run wins.

## 2. Set the connection

```bash
export OMNIDDI_ENDPOINT="https://omniddi-test.example.com"
```

```bash
export OMNIDDI_USERNAME="terraform-automation@example.com"
```

Read the password without echoing it or saving it in shell history:

```bash
read -rs OMNIDDI_PASSWORD && export OMNIDDI_PASSWORD
```

Other provider settings work the same way, for example `OMNIDDI_NETWORK_SPACE_ID` or `OMNIDDI_CA_CERT_PEM`.

Or keep them in a file of `NAME=value` lines and let the scripts load it:

```bash
export OMNIDDI_ENV_FILE="$PWD/e2e/vm.env"
```

The file needs `OMNIDDI_ENDPOINT`, `OMNIDDI_USERNAME` and `OMNIDDI_PASSWORD`, and may set `SMOKE_DNS_VIEW` or `E2E_DNS_VIEW` for the write test. Never commit it.

## 3. Read-only smoke test

Signs in and lists network spaces, DNS views and server groups. It changes nothing on the server.

```bash
local-tests/scripts/smoke.sh read
```

## 4. Write smoke test

Creates a throwaway zone `tf-smoke-<timestamp>.example.test` with an A record and a TXT record, checks that a second `terraform plan` shows no changes, then deletes the zone, also when a step fails.

Use a test server, and pick a DNS view the account may write to:

```bash
export SMOKE_DNS_VIEW="default"
```

```bash
local-tests/scripts/smoke.sh write
```

Set `SMOKE_ZONE_NAME` to use a fixed zone name instead.

## 5. Clean up

Removes the mirror, the generated Terraform settings and the local state files:

```bash
local-tests/scripts/clean.sh
```

## How it works

- `install-release.sh` downloads the zip for your platform, `SHA256SUMS` and `SHA256SUMS.sig` from the GitHub release. It checks the checksum file is signed by the release key in `keys/` (fingerprint `FCE4000F3AA472FC195854B82206E7801DE68D5E`) and that the zip matches it, then copies the zip to `.mirror/registry.terraform.io/checkred/omniddi/`. It writes `.terraformrc` with a `filesystem_mirror` for `checkred/omniddi` only; every other provider still comes from the Registry.
- `install-local-build.sh` runs `make build` and writes `.terraformrc` with `dev_overrides`, so Terraform uses `src/terraform-provider-omniddi` directly and `terraform init` is skipped.
- `smoke.sh` sets `TF_CLI_CONFIG_FILE` to that `.terraformrc` only for its own Terraform commands. Your normal Terraform setup is not changed.

To use the installed provider in your own configuration, point Terraform at the same file:

```bash
export TF_CLI_CONFIG_FILE="$PWD/local-tests/.terraformrc"
```

## Troubleshooting

| Message | Cause |
|---|---|
| `could not download v…` | The release or your platform's zip does not exist, or `gh` is not signed in |
| `not signed by the release key` | The checksum file was not signed with the release key; do not use that release |
| `the second plan wants changes` | The provider reports drift right after apply; run `terraform -chdir=local-tests/smoke/write plan` to see which attribute |
| `Failed to query available provider packages` | Run the install script again; in release mode the mirror holds only the version you installed |
| `could not delete tf-smoke-…` | Cleanup failed three times; the last error is printed above it. Run `SMOKE_ZONE_NAME=<that name> local-tests/scripts/smoke.sh write` again, or delete the zone in OmniDDI |
