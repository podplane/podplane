#!/usr/bin/env bash
# Podplane <https://podplane.dev>
# Copyright The Podplane Authors
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail

# Keeps schemas/podplane.oidc.truster.schema.json a verbatim copy of the
# configuration schema for the Truster release pinned by TrusterModuleVersion
# in internal/tfgen/oidc.go. "update" rewrites the copy; "check" fails when it
# differs from upstream.
action=${1:?usage: truster-schema.sh update|check}
copy=schemas/podplane.oidc.truster.schema.json
version=$(sed -n 's/^const TrusterModuleVersion = "\(.*\)"$/\1/p' internal/tfgen/oidc.go)
if [ -z "$version" ]; then
	echo "TrusterModuleVersion not found in internal/tfgen/oidc.go" >&2
	exit 1
fi
major=${version%%.*}
url="https://raw.githubusercontent.com/truster-dev/truster/v${version}/schema/v${major}/config.schema.json"

upstream=$(mktemp)
trap 'rm -f "$upstream"' EXIT
curl -fsSL "$url" -o "$upstream"

case "$action" in
update)
	cp "$upstream" "$copy"
	echo "Updated $copy from Truster v$version"
	;;
check)
	if ! cmp -s "$upstream" "$copy"; then
		echo "$copy differs from Truster v$version; run make truster-schema" >&2
		exit 1
	fi
	echo "$copy matches Truster v$version"
	;;
*)
	echo "usage: truster-schema.sh update|check" >&2
	exit 1
	;;
esac
