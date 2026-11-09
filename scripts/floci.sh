#!/usr/bin/env bash
# Podplane <https://podplane.dev>
# Copyright The Podplane Authors
# SPDX-License-Identifier: Apache-2.0

# Start, stop, or destroy the local Floci AWS emulator using Podman or Docker Compose.

set -euo pipefail

fail() { echo "$*" >&2; exit 1; }
action=${1:-}
case "$action" in
  start|stop|destroy) ;;
  *) fail "Usage: floci.sh start|stop|destroy" ;;
esac

runner=${FLOCI_RUNNER:-}
if [[ -z "$runner" ]]; then
  if command -v podman >/dev/null 2>&1; then
    runner=podman
  elif command -v docker >/dev/null 2>&1; then
    runner=docker
  else
    fail "Install Podman (https://podman.io) or Docker (https://docs.docker.com/get-started/get-docker/) first."
  fi
fi
case "$runner" in
  podman|docker) ;;
  *) fail "FLOCI_RUNNER must be podman or docker." ;;
esac
command -v "$runner" >/dev/null 2>&1 || fail "$runner is not installed."
"$runner" info >/dev/null 2>&1 || fail "$runner is not reachable. Start its engine (on macOS: podman machine start, or open Docker Desktop). No fallback was attempted."
"$runner" compose version >/dev/null 2>&1 || fail "$runner Compose is unavailable. Install a Compose provider for Podman, or the Docker Compose plugin."

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose=("$runner" compose --project-name podplane-floci --file "$root/scripts/compose.floci.yaml")
echo "Using $runner Compose (project podplane-floci)."
if [[ "$action" == stop || "$action" == destroy ]]; then
  "${compose[@]}" stop
  if [[ "$action" == destroy ]]; then
    "${compose[@]}" down --volumes
    echo "Floci removed, including its container, network, and persisted data."
  else
    echo "Floci stopped; its container, network, and data volume are preserved."
  fi
  exit 0
fi

command -v curl >/dev/null 2>&1 || fail "curl is required to check Floci readiness."
if ! "${compose[@]}" up -d --no-recreate; then
  fail "Could not start Floci. Check the error above; an existing manual Floci may already own port 4566."
fi
deadline=$((SECONDS + 60))
while (( SECONDS < deadline )); do
  if curl --fail --silent --max-time 2 --data 'Action=GetCallerIdentity&Version=2011-06-15' \
    http://127.0.0.1:4566/ | grep -q '<GetCallerIdentityResponse'; then
    echo "Floci is ready at http://localhost:4566 (EC2 mock mode)."
    exit 0
  fi
  sleep 1
done
"${compose[@]}" logs --tail 50 >&2 || true
fail "Floci did not become ready within 60 seconds. Check the logs above and whether another service owns port 4566."
