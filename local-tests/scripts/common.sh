#!/usr/bin/env bash
# Shared paths and helpers for the local-tests scripts.
# shellcheck disable=SC2034

LOCAL_TESTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "${LOCAL_TESTS_DIR}/.." && pwd)"
CLI_CONFIG="${LOCAL_TESTS_DIR}/.terraformrc"
MIRROR_DIR="${LOCAL_TESTS_DIR}/.mirror"
MODE_FILE="${LOCAL_TESTS_DIR}/.install-mode"

PROVIDER_ADDRESS="registry.terraform.io/checkred/omniddi"
PROVIDER_NAME="terraform-provider-omniddi"
GITHUB_REPO="${OMNIDDI_GITHUB_REPO:-CheckRed/terraform-provider-omniddi}"
RELEASE_KEY_FILE="${LOCAL_TESTS_DIR}/keys/omniddi-release-signing-key.asc"
RELEASE_KEY_FINGERPRINT="FCE4000F3AA472FC195854B82206E7801DE68D5E"

die() {
  echo "error: $*" >&2
  exit 1
}

info() {
  echo "==> $*"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "'$1' is required but not installed${2:+ ($2)}"
}

platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    i386 | i686) arch=386 ;;
    armv7l) arch=arm ;;
    *) die "unsupported CPU architecture: $(uname -m)" ;;
  esac
  echo "${os}_${arch}"
}

load_env_file() {
  [[ -n "${OMNIDDI_ENV_FILE:-}" ]] || return 0
  [[ -f "${OMNIDDI_ENV_FILE}" ]] || die "OMNIDDI_ENV_FILE ${OMNIDDI_ENV_FILE} does not exist"
  set -a
  # shellcheck source=/dev/null
  source "${OMNIDDI_ENV_FILE}"
  set +a
}

repo_version() {
  tr -d '[:space:]' <"${REPO_ROOT}/VERSION"
}
