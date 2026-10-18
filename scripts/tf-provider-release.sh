#!/usr/bin/env bash
# Podplane <https://podplane.dev>
# Copyright The Podplane Authors
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail

# Run from the source repository. Only "publish" writes to Git or GitHub.
action=${1:?usage: tf-provider-release.sh metadata|check|publish SOURCE_TAG [PUBLICATION_CHECKOUT]}
tag=${2:?source tag required}
repo=podplane/terraform-provider-podplane

fail() {
  echo "$*" >&2
  exit 1
}

case $action in
  metadata|check|publish) ;;
  *) fail "Unknown action: $action (expected metadata, check, or publish)" ;;
esac

# Require a clean source checkout at the requested release tag.
version=$(go run ./scripts/tf-provider-version "$tag")
commit=$(git rev-parse HEAD)
[[ $tag == v* && $version != 0.0.0-dev ]] || fail "Expected a release tag prefixed with v: $tag"
[[ $(git rev-parse "refs/tags/$tag^{commit}") == "$commit" ]] || fail "Source HEAD does not match $tag"
status=$(git status --porcelain --untracked-files=all)
[[ -z $status ]] || fail 'Source checkout has uncommitted changes'

metadata=$(jq -n --arg tag "$tag" --arg commit "$commit" --arg version "$version" '{
  source_repository: "podplane/podplane",
  source_tag: $tag,
  source_commit: $commit,
  provider_version: $version
}')

if [[ $action == metadata ]]; then
  printf '%s\n' "$metadata"
  exit
fi

checkout=${3:?publication checkout required}
provider_tag=v$version
prefix=terraform-provider-podplane_$version
status=$(git -C "$checkout" status --porcelain --untracked-files=all)
[[ -z $status ]] || fail 'Publication checkout has uncommitted changes'
case $(git -C "$checkout" remote get-url origin) in
  "https://github.com/$repo"|"https://github.com/$repo.git"|"git@github.com:$repo.git") ;;
  *) fail "Publication origin must be $repo" ;;
esac
branch=$(git -C "$checkout" symbolic-ref --short HEAD)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Prepare exactly the files managed in the publication repository.
publication="$tmp/publication"
mkdir -p "$publication/docs"
printf '%s\n' "$metadata" > "$publication/release-metadata.json"
cp cmd/terraform-provider-podplane/docs.md "$publication/docs/index.md"
cp cmd/terraform-provider-podplane/terraform-registry-manifest.json "$publication/"

# Compare the managed publication files, not the current publication branch:
# it may already contain a newer release when an older workflow is retried.
has_tag=$(git -C "$checkout" tag --list "$provider_tag")
if [[ -n $has_tag ]]; then
  mkdir "$tmp/tag"
  git -C "$checkout" archive "$provider_tag" docs terraform-registry-manifest.json release-metadata.json | tar -x -C "$tmp/tag"
  diff -r "$publication" "$tmp/tag"
fi

# Listing distinguishes a missing release from authentication/network failures.
gh api --paginate --slurp "repos/$repo/releases" > "$tmp/releases.json"
state=$(jq -r --arg tag "$provider_tag" '
  [.[][] | select(.tag_name == $tag)] |
  if length == 0 then "new"
  elif .[0].draft then "draft"
  else "published"
  end
' "$tmp/releases.json")
if [[ $state == draft ]]; then
  fail 'Provider draft exists; inspect and recover it manually. No assets will be replaced.'
fi

# Both reused and newly prepared releases must contain this exact asset set.
{
  for platform in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64; do
    printf '%s\n' \
      "${prefix}_${platform}.zip" \
      "${prefix}_${platform}.zip.spdx.json" \
      "${prefix}_${platform}_binary.spdx.json"
  done
  printf '%s\n' \
    "${prefix}_manifest.json" \
    release-metadata.json \
    "${prefix}_SHA256SUMS" \
    "${prefix}_SHA256SUMS.sig" \
    "${prefix}_SHA512SUMS" \
    "${prefix}_SHA512SUMS.bundle"
} | LC_ALL=C sort > "$tmp/expected"

check_assets() {
  find "$1" -type f -exec basename {} \; | LC_ALL=C sort > "$tmp/actual"
  diff "$tmp/expected" "$tmp/actual"
}

if [[ $state == published ]]; then
  [[ -n $has_tag ]] || fail "Published release is missing local tag $provider_tag; fetch publication tags"
  mkdir "$tmp/assets"
  gh release download "$provider_tag" --repo "$repo" --dir "$tmp/assets"
  check_assets "$tmp/assets"
  cmp "$publication/release-metadata.json" "$tmp/assets/release-metadata.json"
  cmp "$publication/terraform-registry-manifest.json" "$tmp/assets/${prefix}_manifest.json"
  (
    cd "$tmp/assets"
    cosign verify-blob --bundle "${prefix}_SHA512SUMS.bundle" \
      --certificate-identity "https://github.com/podplane/podplane/.github/workflows/release.yaml@refs/tags/$tag" \
      --certificate-oidc-issuer https://token.actions.githubusercontent.com "${prefix}_SHA512SUMS"
    shasum -a 512 -c "${prefix}_SHA512SUMS"
    shasum -a 256 -c "${prefix}_SHA256SUMS"
    gpg --batch --status-fd 1 --verify "${prefix}_SHA256SUMS.sig" "${prefix}_SHA256SUMS" > "$tmp/gpg-status"
    # GPG may report the signing subkey first and its primary key last.
    awk -v key="${GPG_FINGERPRINT:?}" '
      $2 == "VALIDSIG" && ($3 == key || $NF == key) { valid = 1 }
      END { exit !valid }
    ' "$tmp/gpg-status"
  )
  echo 'Provider release verified; reusing immutable assets.' >&2
  echo 'publish=false' >> "${GITHUB_OUTPUT:?}"
  exit
fi

if [[ $action == check ]]; then
  echo 'publish=true' >> "${GITHUB_OUTPUT:?}"
  exit
fi

# GoReleaser owns archives, SBOMs and Registry checksums/signatures. Add the
# SHA-512/Cosign checksum bundle required by the project release standard.
out=${TF_PROVIDER_RELEASE_ASSETS:?TF provider asset directory required}
cp "$publication/terraform-registry-manifest.json" "$out/${prefix}_manifest.json"
cp "$publication/release-metadata.json" "$out/release-metadata.json"
(
  cd "$out"
  shasum -a 256 -c "${prefix}_SHA256SUMS"
  shasum -a 512 ./*.zip ./*.json ./*_SHA256SUMS ./*_SHA256SUMS.sig > "${prefix}_SHA512SUMS"
  cosign sign-blob --yes --bundle "${prefix}_SHA512SUMS.bundle" "${prefix}_SHA512SUMS"
)
check_assets "$out"

if [[ -z $has_tag ]]; then
  rm -rf "$checkout/docs"
  cp -R "$publication/." "$checkout/"
  git -C "$checkout" add -- docs terraform-registry-manifest.json release-metadata.json
  git -C "$checkout" -c user.name=podplane-release-bot -c user.email=release-bot@podplane.dev \
    commit -s -m "Publish provider $version from $tag ($commit)"
  git -C "$checkout" tag "$provider_tag"
  git -C "$checkout" push --atomic origin "HEAD:refs/heads/$branch" "refs/tags/$provider_tag"
fi
prerelease=()
if [[ $version == *-* ]]; then prerelease=(--prerelease); fi
gh release create "$provider_tag" --repo "$repo" --verify-tag --draft "${prerelease[@]}" \
  --title "Provider $version" \
  --notes "Source: https://github.com/podplane/podplane/commit/$commit ($tag). See release-metadata.json."
gh release upload "$provider_tag" "$out"/* --repo "$repo"
gh release edit "$provider_tag" --repo "$repo" --draft=false
