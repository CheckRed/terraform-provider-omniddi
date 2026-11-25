#!/usr/bin/env bash
# Builds the provider from this checkout and points Terraform at the build with dev_overrides.
set -euo pipefail
# shellcheck source=local-tests/scripts/common.sh
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"

require_command go "https://go.dev/dl"
require_command make

info "Building ${PROVIDER_NAME} $(repo_version) from source"
make -C "${REPO_ROOT}" build >/dev/null

cat >"${CLI_CONFIG}" <<EOF
provider_installation {
  dev_overrides {
    "checkred/omniddi" = "${REPO_ROOT}/src"
  }
  direct {}
}
EOF
echo "source $(repo_version)" >"${MODE_FILE}"

info "Built ${REPO_ROOT}/src/${PROVIDER_NAME}"
info "Terraform will now use this build for checkred/omniddi; rerun this script after code changes"
