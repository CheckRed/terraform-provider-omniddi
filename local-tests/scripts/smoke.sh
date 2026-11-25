#!/usr/bin/env bash
# Runs a smoke test against a real OmniDDI server: "read" only reads, "write" creates and then deletes a test zone.
set -euo pipefail
# shellcheck source=local-tests/scripts/common.sh
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"

mode="${1:-}"
[[ "${mode}" == "read" || "${mode}" == "write" ]] || die "usage: $0 read|write"

require_command terraform "https://developer.hashicorp.com/terraform/install"
[[ -f "${CLI_CONFIG}" && -f "${MODE_FILE}" ]] ||
  die "no provider installed; run scripts/install-release.sh or scripts/install-local-build.sh first"
load_env_file
for var in OMNIDDI_ENDPOINT OMNIDDI_USERNAME OMNIDDI_PASSWORD; do
  [[ -n "${!var:-}" ]] || die "${var} is not set"
done

export TF_CLI_CONFIG_FILE="${CLI_CONFIG}"
export CHECKPOINT_DISABLE=1
export TF_IN_AUTOMATION=1
dir="${LOCAL_TESTS_DIR}/smoke/${mode}"
read -r install_kind install_version <"${MODE_FILE}"
tf() { terraform -chdir="${dir}" "$@"; }

info "Testing the ${install_kind} provider ${install_version} against ${OMNIDDI_ENDPOINT} (${mode})"
if [[ "${install_kind}" == "release" ]]; then
  tf init -input=false -upgrade >/dev/null
fi

if [[ "${mode}" == "read" ]]; then
  tf apply -input=false -auto-approve
  info "Read smoke test passed"
  exit 0
fi

export TF_VAR_view_name="${SMOKE_DNS_VIEW:-${E2E_DNS_VIEW:-}}"
[[ -n "${TF_VAR_view_name}" ]] || die "set SMOKE_DNS_VIEW to the DNS view to create the test zone in"
export TF_VAR_zone_name="${SMOKE_ZONE_NAME:-tf-smoke-$(date +%s).example.test}"

destroy() {
  local log attempt
  log="$(mktemp)"
  for attempt in 1 2 3; do
    info "Deleting the test zone ${TF_VAR_zone_name} (attempt ${attempt} of 3)"
    if tf destroy -input=false -auto-approve >"${log}" 2>&1; then
      rm "${log}"
      return 0
    fi
    sleep 15
  done
  tail -n 20 "${log}" >&2
  rm "${log}"
  echo "error: could not delete ${TF_VAR_zone_name}; run scripts/smoke.sh write again with SMOKE_ZONE_NAME=${TF_VAR_zone_name} or delete it in OmniDDI" >&2
  return 1
}

cleanup() {
  local status=$?
  destroy || status=1
  exit "${status}"
}
trap cleanup EXIT

info "Creating test zone ${TF_VAR_zone_name} in view ${TF_VAR_view_name}"
tf apply -input=false -auto-approve

info "Checking a second plan shows no changes"
set +e
tf plan -input=false -detailed-exitcode >/dev/null
plan_status=$?
set -e
case "${plan_status}" in
  0) info "No drift after apply" ;;
  2) die "the second plan wants changes; the provider is not idempotent for this configuration" ;;
  *) die "terraform plan failed" ;;
esac

info "Write smoke test passed"
