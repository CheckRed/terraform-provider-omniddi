#!/usr/bin/env bash
# Downloads a GitHub release (latest by default), installs it locally and runs the smoke tests against it.
set -euo pipefail
# shellcheck source=local-tests/scripts/common.sh
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"

release="${1:-latest}"
scripts="${LOCAL_TESTS_DIR}/scripts"
load_env_file

"${scripts}/install-release.sh" "${release}"
"${scripts}/smoke.sh" read

if [[ -n "${SMOKE_DNS_VIEW:-${E2E_DNS_VIEW:-}}" ]]; then
  "${scripts}/smoke.sh" write
else
  info "Skipping the write smoke test; set SMOKE_DNS_VIEW to run it"
fi

info "All smoke tests passed for $(cut -d' ' -f2 "${MODE_FILE}")"
