#!/usr/bin/env bash
# Decides whether the current commit should be released, from VERSION and CHANGELOG.md.
set -euo pipefail

release_branch="${RELEASE_BRANCH:-master}"
event="${EVENT:?EVENT is required}"
ref_type="${REF_TYPE:?REF_TYPE is required}"
ref_name="${REF_NAME:?REF_NAME is required}"
sha="${SHA:?SHA is required}"
repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
out="${GITHUB_OUTPUT:-/dev/stdout}"

emit() { printf '%s=%s\n' "$1" "$2" >>"$out"; }
skip() {
  echo "::notice title=Release skipped::$1"
  emit release false
  exit 0
}
fail() {
  echo "::error title=Release refused::$1" >&2
  exit 1
}

make --no-print-directory version-check || fail "VERSION or CHANGELOG.md is invalid"
version="$(tr -d '[:space:]' <VERSION)"
tag="v${version}"

if [[ "$ref_type" == "tag" && "$ref_name" != "$tag" ]]; then
  fail "Tag ${ref_name} does not match VERSION ${version}; expected ${tag}"
fi

if grep -q "^## ${version} (Unreleased)" CHANGELOG.md; then
  if [[ "$event" == "push" && "$ref_type" == "branch" ]]; then
    skip "CHANGELOG.md marks ${version} as Unreleased; run 'make changelog-date' to date it and publish ${tag}"
  fi
  fail "CHANGELOG.md still marks ${version} as Unreleased"
fi

if api_err="$(gh api "repos/${repo}/releases/tags/${tag}" --silent 2>&1)"; then
  skip "${tag} is already released"
elif [[ "$api_err" != *"Not Found"* ]]; then
  fail "Could not check for an existing ${tag} release: ${api_err}"
fi

release_sha="$sha"
create_tag=true
if tag_sha="$(git rev-parse -q --verify "refs/tags/${tag}^{commit}")"; then
  release_sha="$tag_sha"
  create_tag=false
  echo "::notice::${tag} already exists at ${tag_sha}; releasing that commit"
fi

git fetch --quiet --no-tags origin "+refs/heads/${release_branch}:refs/remotes/origin/${release_branch}"
git merge-base --is-ancestor "$release_sha" "origin/${release_branch}" ||
  fail "Commit ${release_sha} is not on ${release_branch}"

echo "::notice title=Releasing ${tag}::commit ${release_sha}, create tag: ${create_tag}"
emit release true
emit version "$version"
emit tag "$tag"
emit sha "$release_sha"
emit create_tag "$create_tag"
