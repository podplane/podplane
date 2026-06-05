#!/bin/bash -e
# Podplane VM userdata (rendered).
# Provider: {{.Env.ProviderKind}}
# Cluster ID: {{.Env.ClusterID}}
# Instance ID: {{.Env.InstanceID}}
# Manifest version: {{.Manifest.VMConfig.Version}}
# OS: {{.Manifest.VMConfig.OS.Name}}
# Arch: {{.Manifest.VMConfig.OS.Arch}}
# Cluster bucket names (cluster-prefixed):
#   netsy={{.Env.NetsyBucket}}
#   registry={{.Env.RegistryBucket}}
#   telemetry={{.Env.TelemetryBucket}}
# OIDC Issuer: {{.Env.OIDCIssuer}}
{{- if .DepsMirrorURL}}
# Deps Mirror={{.DepsMirrorURL}}
{{- end}}
set -euo pipefail

echo "Podplane cloud-init user-data script has started."
# ----------------------------------------------------------------------------

# --- 1. Configure hostname --------------------------------------------------

hostnamectl set-hostname {{.Env.InstanceID}}
{{if eq .Env.ProviderKind "local"}}echo "debian:devonly" | chpasswd
{{end}}

VMCONFIG_ALREADY_INSTALLED=false
if [ -f /opt/podplane/share/vmconfig-installed.json ]; then
  VMCONFIG_ALREADY_INSTALLED=true
  echo "vmconfig is already installed; refreshing bootstrap files only."
fi

ensure_nstance_nonce_permissions() {
  if [ -f /opt/nstance-agent/identity/nonce.jwt ]; then
    chmod 0600 /opt/nstance-agent/identity/nonce.jwt
    chown nstance-agent:nstance-agent /opt/nstance-agent/identity/nonce.jwt
  fi
}

# --- 2. Download and verify dependencies ------------------------------------

if [ "$VMCONFIG_ALREADY_INSTALLED" = "true" ]; then
  echo "Skipping dependency download; vmconfig is already installed."
else
  ARTIFACTS_DIR="/opt/podplane/artifacts"
  mkdir -p "$ARTIFACTS_DIR"

  echo "Downloading {{len (.Manifest.InstallItems .ManifestFilter)}} dependencies..."
  curl -sfL --parallel --parallel-max 10 --parallel-immediate \
{{- range (.Manifest.InstallItems .ManifestFilter)}}
    -o "${ARTIFACTS_DIR}/{{.LocalFilename}}" "{{.ResolveURL $.DepsMirrorURL}}" \
{{- end}}
    >/dev/null

  echo "Verifying checksums..."
  while read -r digest filename; do
    case "$digest" in
      sha256:*) echo "${digest#sha256:}  ${filename}" | sha256sum -c --quiet ;;
      sha512:*) echo "${digest#sha512:}  ${filename}" | sha512sum -c --quiet ;;
      *) echo "Unsupported digest algorithm for ${filename}: ${digest}" >&2; exit 1 ;;
    esac
  done <<CHECKSUMS
{{- range (.Manifest.InstallItems .ManifestFilter)}}
{{.Dep.Digest}}  ${ARTIFACTS_DIR}/{{.LocalFilename}}
{{- end}}
CHECKSUMS

# --- 3. Extract vmconfig tarball --------------------------------------------
{{if .Manifest.HasVMConfigDep .ManifestFilter}}
  echo "Extracting vmconfig.tar.gz..."
  tar -xzf "${ARTIFACTS_DIR}/vmconfig.tar.gz" -C /
{{else}}
# skipped
{{- end}}
fi

# --- 4. Write user-data environment file ------------------------------------

echo "Writing user-data.env file..."
mkdir -p /opt/podplane/etc
cat > /opt/podplane/etc/user-data.env <<'USERDATA_ENV'
SSH_AUTHORIZED_KEY='{{.Env.SSHAuthorizedKey}}'

INSTANCE_ID='{{.Env.InstanceID}}'
CLUSTER_ID='{{.Env.ClusterID}}'

PROVIDER_KIND='{{.Env.ProviderKind}}'
PROVIDER_REGION='{{.Env.ProviderRegion}}'
PROVIDER_ZONE='{{.Env.ProviderZone}}'
PROVIDER_INSTANCE_TYPE='{{.Env.ProviderInstanceType}}'
AWS_ACCOUNT_ID='{{.Env.AWSAccountID}}'
GOOGLE_PROJECT_ID='{{.Env.GoogleProjectID}}'

OIDC_ISSUER='{{.Env.OIDCIssuer}}'
OIDC_CUSTOM_CA='{{.Env.OIDCCustomCA}}'
OIDC_CA_FILE='{{.Env.OIDCCAFile}}'

KUBE_LOG_LEVEL='{{.Env.KubeLogLevel}}'
KUBE_API_PUBLIC_HOSTNAME='{{.Env.KubeAPIPublicHostname}}'
KUBE_API_PORT='{{.Env.KubeAPIPort}}'
KUBE_API_ETCD_SERVERS='{{.Env.KubeAPIEtcdServers}}'

NSTANCE_CA_CERT='{{.Env.NstanceCACert}}'
NSTANCE_SERVER_REGISTRATION_ADDR='{{.Env.NstanceServerRegistrationAddr}}'
NSTANCE_SERVER_AGENT_ADDR='{{.Env.NstanceServerAgentAddr}}'

NETSY_BUCKET='{{.Env.NetsyBucket}}'
NETSY_ENDPOINT='{{.Env.NetsyEndpoint}}'
NETSY_REGION='{{.Env.NetsyRegion}}'
NETSY_ACCESS_KEY_ID='{{.Env.NetsyAccessKeyID}}'
NETSY_SECRET_ACCESS_KEY='{{.Env.NetsySecretAccessKey}}'

TELEMETRY_BUCKET='{{.Env.TelemetryBucket}}'
TELEMETRY_ENDPOINT='{{.Env.TelemetryEndpoint}}'
TELEMETRY_REGION='{{.Env.TelemetryRegion}}'
TELEMETRY_LOG_SERVICES='{{.Env.TelemetryLogServices}}'
TELEMETRY_LOG_CLOUDINIT='{{.Env.TelemetryLogCloudinit}}'
TELEMETRY_ACCESS_KEY_ID='{{.Env.TelemetryAccessKeyID}}'
TELEMETRY_SECRET_ACCESS_KEY='{{.Env.TelemetrySecretAccessKey}}'

REGISTRY_ENABLED='{{.Env.RegistryEnabled}}'
REGISTRY_BUCKET='{{.Env.RegistryBucket}}'
REGISTRY_HOSTNAME='{{.Env.RegistryHostname}}'
REGISTRY_ENDPOINT='{{.Env.RegistryEndpoint}}'
REGISTRY_REGION='{{.Env.RegistryRegion}}'
REGISTRY_ACCESS_KEY_ID='{{.Env.RegistryAccessKeyID}}'
REGISTRY_SECRET_ACCESS_KEY='{{.Env.RegistrySecretAccessKey}}'
AWS_S3_USE_PATH_STYLE='{{.Env.AWSS3UsePathStyle}}'
USERDATA_ENV
chmod 0600 /opt/podplane/etc/user-data.env

# --- 5. Write sensitive nstance bootstrap files -----------------------------

{{- if .NstanceRegistrationNonceJWT}}
echo "Writing nstance registration nonce file..."
mkdir -p /opt/nstance-agent/identity
cat > /opt/nstance-agent/identity/nonce.jwt <<'NSTANCE_NONCE_JWT'
{{.NstanceRegistrationNonceJWT}}
NSTANCE_NONCE_JWT
chmod 0600 /opt/nstance-agent/identity/nonce.jwt
{{else}}
# skipped
{{- end}}

# -- 6. Run install.sh -------------------------------------------------------

if [ "$VMCONFIG_ALREADY_INSTALLED" = "true" ]; then
  echo "Skipping install.sh, configure.sh, and restart.sh because vmconfig is already installed."
  ensure_nstance_nonce_permissions
  if systemctl list-unit-files nstance-agent.service >/dev/null 2>&1; then
    systemctl restart nstance-agent || true
  fi
  echo "Podplane cloud-init user-data script has completed successfully."
  exit 0
fi

{{if not (.Manifest.HasVMConfigDep .ManifestFilter)}}
{{if eq .Env.ProviderKind "local"}}
if ! command -v rsync >/dev/null 2>&1; then
  echo "Installing rsync for local vmconfig development sync..."
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends rsync
fi
if [ ! -x /opt/podplane/bin/install.sh ]; then
  echo "No vmconfig package was included in the vmconfig manifest."
  echo "Sync local vmconfig into the VM, then re-run this user-data script."
  exit 0
fi
{{end}}
{{end}}

echo "Running install.sh..."
chmod +x /opt/podplane/bin/install.sh
/opt/podplane/bin/install.sh

# --- 7. Run configure.sh ----------------------------------------------------
echo "Running configure.sh..."
chmod +x /opt/podplane/bin/configure.sh
/opt/podplane/bin/configure.sh
{{if eq .Env.ProviderKind "local"}}
# --- Local provider VM preparation ------------------------------------------

echo "Applying local provider VM preparation..."
set +u
# shellcheck source=/dev/null
source /opt/podplane/etc/user-data.env
# shellcheck source=/dev/null
source /opt/podplane/etc/detected.env
# shellcheck source=/dev/null
source /opt/podplane/etc/mutable.env
set -u

for url in \
  "${OIDC_ISSUER:-}" \
  "${NSTANCE_SERVER_REGISTRATION_ADDR:-}" \
  "${NSTANCE_SERVER_AGENT_ADDR:-}" \
  "${NETSY_ENDPOINT:-}" \
  "${TELEMETRY_ENDPOINT:-}" \
  "${REGISTRY_ENDPOINT:-}"
do
  [ -n "$url" ] || continue
  host="${url#*://}"
  host="${host%%/*}"
  host="${host##*@}"
  if [[ "$host" == \[*\]* ]]; then
    host="${host%%]*}"
    host="${host#[}"
  elif [[ "$host" == *:*:* ]]; then
    continue
  else
    host="${host%%:*}"
  fi
  [ -n "$host" ] || continue
  [ "$host" = "localhost" ] && continue
  [[ "$host" =~ ^[0-9.]+$ ]] && continue
  if ! grep -Eq "[[:space:]]${host}([[:space:]]|$)" /etc/hosts; then
    echo "10.0.2.2 ${host}" >> /etc/hosts
  fi
done

if [ -f /etc/systemd/resolved.conf ] && ! grep -qF "DNS=1.1.1.1" /etc/systemd/resolved.conf; then
  echo "DNS=1.1.1.1" >> /etc/systemd/resolved.conf
  systemctl restart systemd-resolved
fi

if [ -n "${INSTANCE_NETDEV:-}" ] && ! ip -6 addr show dev "$INSTANCE_NETDEV" | grep -q 'fd00::1/64'; then
  ip -6 addr add fd00::1/64 dev "$INSTANCE_NETDEV"
fi

if [ -n "${OIDC_CUSTOM_CA:-}" ] && [ -n "${OIDC_CA_FILE:-}" ]; then
  echo "${OIDC_CUSTOM_CA}" | base64 -d > "$OIDC_CA_FILE"
  chmod 0644 "$OIDC_CA_FILE"
fi

systemctl disable unattended-upgrades || true
{{end}}
# --- 8. Restart services ----------------------------------------------------
echo "Running restart.sh..."
chmod +x /opt/podplane/bin/restart.sh
/opt/podplane/bin/restart.sh

# ----------------------------------------------------------------------------
echo "Podplane cloud-init user-data script has completed successfully."
