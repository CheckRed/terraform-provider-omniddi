#!/usr/bin/env bash
# Promotes a verified pre-release to the full, latest release.
set -euo pipefail

tag="${TAG:?TAG is required}"
repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
release_branch="${RELEASE_BRANCH:-master}"

fail() {
  echo "::error title=Promotion refused::$1" >&2
  exit 1
}

[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
  fail "${tag} is not a stable version tag like v1.2.3; versions with a suffix stay pre-releases"

release_json="$(gh release view "$tag" --repo "$repo" --json isDraft,isPrerelease,assets 2>&1)" ||
  fail "No release found for ${tag}: ${release_json}"

[[ "$(jq -r '.isDraft' <<<"$release_json")" == "false" ]] || fail "${tag} is still a draft"
if [[ "$(jq -r '.isPrerelease' <<<"$release_json")" == "false" ]]; then
  echo "::notice title=Nothing to do::${tag} is already a full release"
  exit 0
fi

assets="$(jq -r '.assets[].name' <<<"$release_json")"
for suffix in "_SHA256SUMS" "_SHA256SUMS.sig" "_manifest.json"; do
  grep -q -- "${suffix}\$" <<<"$assets" || fail "${tag} is missing its *${suffix} asset"
done
grep -q -- '\.zip$' <<<"$assets" || fail "${tag} has no provider zip archives"

status="$(gh api "repos/${repo}/compare/${release_branch}...${tag}" --jq '.status')" ||
  fail "Could not compare ${tag} with ${release_branch}"
[[ "$status" == "identical" || "$status" == "behind" ]] ||
  fail "${tag} is not on ${release_branch} (compare status: ${status})"

gh release edit "$tag" --repo "$repo" --prerelease=false --latest
echo "::notice title=Released ${tag}::${tag} is now the latest full release"
