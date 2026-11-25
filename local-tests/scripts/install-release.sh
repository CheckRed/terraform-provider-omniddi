#!/usr/bin/env bash
# Installs a GitHub release (latest, pre or a version) into a local mirror, after checking its signature and checksum.
set -euo pipefail
# shellcheck source=local-tests/scripts/common.sh
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"

require_command gh "https://cli.github.com"
require_command gpg "brew install gnupg"
require_command shasum

requested="${1:-latest}"
case "${requested}" in
  latest)
    tag="$(gh release view --repo "${GITHUB_REPO}" --json tagName --jq '.tagName' 2>/dev/null)" ||
      die "no full release found in ${GITHUB_REPO}; use 'pre' for the newest pre-release"
    ;;
  pre)
    tag="$(gh release list --repo "${GITHUB_REPO}" --exclude-drafts --limit 1 --json tagName --jq '.[0].tagName // empty')"
    [[ -n "${tag}" ]] || die "no releases found in ${GITHUB_REPO}"
    ;;
  *)
    tag="v${requested#v}"
    ;;
esac
version="${tag#v}"
target="$(platform)"
zip="${PROVIDER_NAME}_${version}_${target}.zip"
sums="${PROVIDER_NAME}_${version}_SHA256SUMS"

work="$(mktemp -d)"
cleanup() {
  gpgconf --homedir "${work}/gnupg" --kill gpg-agent >/dev/null 2>&1 || true
  rm -r "${work}"
}
trap cleanup EXIT

info "Downloading ${tag} for ${target} from ${GITHUB_REPO}"
gh release download "${tag}" --repo "${GITHUB_REPO}" --dir "${work}" \
  --pattern "${zip}" --pattern "${sums}" --pattern "${sums}.sig" ||
  die "could not download ${tag}; check the release exists and has a ${target} build"

info "Checking the checksum file is signed by the release key ${RELEASE_KEY_FINGERPRINT}"
mkdir -m 700 "${work}/gnupg"
GNUPGHOME="${work}/gnupg" gpg --batch --quiet --import "${RELEASE_KEY_FILE}" 2>/dev/null
GNUPGHOME="${work}/gnupg" gpg --batch --status-fd 1 --verify "${work}/${sums}.sig" "${work}/${sums}" 2>/dev/null |
  grep -q "VALIDSIG ${RELEASE_KEY_FINGERPRINT} " ||
  die "${sums} is not signed by the release key; do not use this release"

info "Checking the ${target} zip matches the signed checksum"
(cd "${work}" && grep " ${zip}\$" "${sums}" | shasum -a 256 -c - >/dev/null) ||
  die "${zip} does not match ${sums}; do not use this release"

dest="${MIRROR_DIR}/${PROVIDER_ADDRESS}"
mkdir -p "${dest}"
find "${dest}" -name "${PROVIDER_NAME}_*.zip" -delete
cp "${work}/${zip}" "${dest}/${zip}"

cat >"${CLI_CONFIG}" <<EOF
provider_installation {
  filesystem_mirror {
    path    = "${MIRROR_DIR}"
    include = ["${PROVIDER_ADDRESS}"]
  }
  direct {
    exclude = ["${PROVIDER_ADDRESS}"]
  }
}
EOF
echo "release ${version}" >"${MODE_FILE}"

info "Installed ${tag} into ${dest}"
info "Terraform will now install checkred/omniddi ${version} from this mirror instead of the registry"
