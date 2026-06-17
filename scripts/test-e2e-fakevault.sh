#!/usr/bin/env bash
# Podplane <https://podplane.dev>
# Copyright The Podplane Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

for cmd in bao jq kubectl; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing required command: $cmd" >&2
    exit 1
  fi
done

: "${PORT:?set PORT to the local server HTTPS port}"
CLUSTER="${CLUSTER:-default}"
CONTEXT="${CONTEXT:-local}"
NAMESPACE="${NAMESPACE:-default}"
SERVICE_ACCOUNT="${SERVICE_ACCOUNT:-default}"
ROLE="${ROLE:-$SERVICE_ACCOUNT}"
export BAO_ADDR="https://127.0.0.1:${PORT}/vault/${CLUSTER}"
export BAO_SKIP_VERIFY="${BAO_SKIP_VERIFY:-true}"

echo "testing fakevault at ${BAO_ADDR}"
echo ""

echo "create Kubernetes service-account token: kubectl --context ${CONTEXT} -n ${NAMESPACE} create token ${SERVICE_ACCOUNT}"
JWT="$(kubectl --context "$CONTEXT" -n "$NAMESPACE" create token "$SERVICE_ACCOUNT" --duration=10m)"
echo "✓ service-account token created: ${#JWT} bytes"
echo ""

echo "login with Kubernetes auth: bao write auth/kubernetes/login jwt=<token> role=${ROLE}"
export BAO_TOKEN="$(
	bao write -format=json auth/kubernetes/login jwt="$JWT" role="$ROLE" \
		| jq -r '.auth.client_token'
)"

if [[ -z "$BAO_TOKEN" || "$BAO_TOKEN" == "null" ]]; then
  echo "failed to get token" >&2
  exit 1
fi

echo "✓ login ok: token=${BAO_TOKEN}"
echo ""

echo "write secret/data/apps/app/db password=secret"
write_output="$(bao write secret/data/apps/app/db password=secret)"
echo "$write_output"

echo "✓ write ok"
echo ""

echo "read secret/data/apps/app/db"
password="$(bao read -format=json secret/data/apps/app/db | jq -r '.data.data.password')"

if [[ "$password" != "secret" ]]; then
	echo "read password = ${password}, want secret" >&2
	exit 1
fi

echo "✓ read ok: password=${password}"

echo "list secret/metadata/apps/app"
keys="$({
	bao list -format=json secret/metadata/apps/app \
		| jq -r 'if type == "array" then join(",") else .data.keys | join(",") end'
})"

if [[ "$keys" != "db" ]]; then
  echo "list keys = ${keys}, want db" >&2
  exit 1
fi

echo "✓ list ok: keys=${keys}"
echo ""

echo "delete secret/metadata/apps/app/db"
delete_output="$(bao delete secret/metadata/apps/app/db)"
echo "$delete_output"

echo "✓ delete ok"
echo ""

echo "confirm secret/data/apps/app/db is deleted"
if bao read secret/data/apps/app/db >/dev/null 2>&1; then
	echo "read after delete succeeded, want failure" >&2
	exit 1
fi

echo "✓ confirm delete ok"
echo ""

echo "done."
