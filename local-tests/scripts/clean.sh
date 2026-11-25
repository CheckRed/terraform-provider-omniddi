#!/usr/bin/env bash
# Removes everything the local-tests scripts generated, including local state files.
set -euo pipefail
# shellcheck source=local-tests/scripts/common.sh
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"

for path in "${MIRROR_DIR}" "${CLI_CONFIG}" "${MODE_FILE}" \
  "${LOCAL_TESTS_DIR}"/smoke/*/.terraform \
  "${LOCAL_TESTS_DIR}"/smoke/*/.terraform.lock.hcl \
  "${LOCAL_TESTS_DIR}"/smoke/*/terraform.tfstate \
  "${LOCAL_TESTS_DIR}"/smoke/*/terraform.tfstate.backup; do
  if [[ -e "${path}" ]]; then
    rm -r "${path}"
    info "Removed ${path#"${REPO_ROOT}/"}"
  fi
done
