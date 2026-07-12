#!/bin/bash -e
# Podplane VM userdata (rendered).
# Provider: {{.Provider.Kind}}
# Cluster ID: {{.Cluster.ID}}
# Instance ID: {{.Instance.ID}}
# Manifest version: {{.Manifest.VMConfig.Version}}
# OS: {{.Manifest.VMConfig.OS.Name}}
# Arch: {{.Manifest.VMConfig.OS.Arch}}
{{- if .DepsMirrorURL}}
# Deps Mirror={{.DepsMirrorURL}}
{{- end}}
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

log() {
  printf '[userdata] %s\tts=%s\n' "$*" "$EPOCHREALTIME"
}

log "Podplane cloud-init user-data script has started."

# ----------------------------------------------------------------------------

# --- 1. Configure hostname --------------------------------------------------

hostnamectl set-hostname {{.Instance.ID}}
{{if eq .Provider.Kind "local"}}echo "debian:devonly" | chpasswd
{{end}}

# --- 2. Install immutable SSH keys for early-boot debugging (if provided) ---

IMMUTABLE_SSH_AUTHORIZED_KEYS='{{.ImmutableSSHAuthorizedKeys}}'
if [ -n "$IMMUTABLE_SSH_AUTHORIZED_KEYS" ]; then
  if ! getent group admin >/dev/null; then
    groupadd admin
  fi
  if ! id admin >/dev/null 2>&1; then
    useradd -g admin -m -s /bin/bash admin
  fi
  install -d -m 0700 -o admin -g admin /home/admin/.ssh
  printf '%s\n' "$IMMUTABLE_SSH_AUTHORIZED_KEYS" > /home/admin/.ssh/authorized_keys
  chmod 0600 /home/admin/.ssh/authorized_keys
  chown admin:admin /home/admin/.ssh/authorized_keys
  mkdir -p /etc/sudoers.d
  printf '%s\n' 'admin ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/admin
  chmod 0440 /etc/sudoers.d/admin
fi

# --- 3. Bootstrap provider-specific tools -----------------------------------

{{if eq .Provider.Kind "aws"}}
%{ if var.enable_ssm ~}
log "Ensuring AWS SSM Agent is installed and running..."
if command -v snap >/dev/null 2>&1 && snap list amazon-ssm-agent >/dev/null 2>&1; then
  snap start amazon-ssm-agent
elif dpkg -s amazon-ssm-agent >/dev/null 2>&1; then
  systemctl enable --now amazon-ssm-agent
else
  curl -fsSL -o /tmp/amazon-ssm-agent.deb \
    "https://s3.amazonaws.com/ec2-downloads-windows/SSMAgent/latest/debian_{{.Manifest.VMConfig.OS.Arch}}/amazon-ssm-agent.deb"
  dpkg -i /tmp/amazon-ssm-agent.deb
  rm -f /tmp/amazon-ssm-agent.deb
  systemctl enable --now amazon-ssm-agent
fi
%{ endif ~}
{{end}}

# --- 4. Check connectivity to Nstance Server --------------------------------

REGISTRATION_ADDR="{{.Server.RegistrationAddr}}"
log "Checking connectivity to nstance-server at $REGISTRATION_ADDR..."
attempt=0
while true
do
  attempt=$((attempt + 1))
  if timeout 5 bash -c "echo > /dev/tcp/${REGISTRATION_ADDR%:*}/${REGISTRATION_ADDR##*:}" 2>/dev/null
  then
    log "Connection successful!"
    break
  fi
  retry_in=15
  if [ $attempt -lt 3 ]
  then
    retry_in=3
  fi
  log "Failed to connect to nstance-server at $REGISTRATION_ADDR (attempt $attempt), retrying in $retry_in seconds..."
  sleep $retry_in
done

# --- 5. Download and verify dependencies ------------------------------------

ARTIFACTS_DIR="/opt/podplane/artifacts"
mkdir -p "$ARTIFACTS_DIR"

log "Downloading {{len (.Manifest.InstallItems .ManifestFilter)}} dependencies..."
curl -sfL --parallel --parallel-max 10 --parallel-immediate \
{{- range (.Manifest.InstallItems .ManifestFilter)}}
  -o "${ARTIFACTS_DIR}/{{.LocalFilename}}" "{{.ResolveURL $.DepsMirrorURL}}" \
{{- end}}
  >/dev/null

log "Verifying checksums..."
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

# --- 6. Extract vmconfig tarball --------------------------------------------
{{if .Manifest.HasVMConfigDep .ManifestFilter}}
log "Extracting vmconfig.tar.gz..."
tar -xzf "${ARTIFACTS_DIR}/vmconfig.tar.gz" -C /
{{else}}
# skipped
{{- end}}

# --- 7. Write user-data environment file ------------------------------------

log "Writing user-data.env file..."
mkdir -p /opt/podplane/etc
cat > /opt/podplane/etc/user-data.env <<'USERDATA_ENV'
IMMUTABLE_SSH_AUTHORIZED_KEYS='{{.ImmutableSSHAuthorizedKeys}}'

INSTANCE_ID='{{.Instance.ID}}'
CLUSTER_ID='{{.Cluster.ID}}'

PROVIDER_KIND='{{.Provider.Kind}}'
PROVIDER_REGION='{{.Provider.Region}}'
PROVIDER_ZONE='{{.Provider.Zone}}'
PROVIDER_INSTANCE_TYPE='{{.Instance.Type}}'
AWS_ACCOUNT_ID='{{.AWSAccountID}}'
GOOGLE_PROJECT_ID='{{.GoogleProjectID}}'

NSTANCE_CA_CERT='{{.Cluster.CACert}}'
NSTANCE_SERVER_REGISTRATION_ADDR='{{.Server.RegistrationAddr}}'
NSTANCE_SERVER_AGENT_ADDR='{{.Server.AgentAddr}}'
USERDATA_ENV
chmod 0600 /opt/podplane/etc/user-data.env

# --- 8. Write sensitive nstance bootstrap files -----------------------------

{{- if .Nonce}}
log "Writing nstance registration nonce file..."
mkdir -p /opt/nstance-agent/identity
cat > /opt/nstance-agent/identity/nonce.jwt <<'NSTANCE_NONCE_JWT'
{{.Nonce}}
NSTANCE_NONCE_JWT
cat > /opt/nstance-agent/identity/ca.crt <<'NSTANCE_CA_CERT'
{{.Cluster.CACert}}
NSTANCE_CA_CERT
chmod 0600 /opt/nstance-agent/identity/nonce.jwt /opt/nstance-agent/identity/ca.crt
{{else}}
# skipped
{{- end}}

# --- 9. Run install.sh -------------------------------------------------------

{{if not (.Manifest.HasVMConfigDep .ManifestFilter)}}
{{if eq .Provider.Kind "local"}}
if ! command -v rsync >/dev/null 2>&1; then
  log "Installing rsync for local vmconfig development sync..."
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends rsync
fi
if [ ! -x /opt/podplane/bin/install.sh ]; then
  log "No vmconfig package was included in the vmconfig manifest."
  log "Sync local vmconfig into the VM, then re-run this user-data script."
  exit 0
fi
{{end}}
{{end}}

log "Running install.sh..."
chmod +x /opt/podplane/bin/install.sh
/opt/podplane/bin/install.sh

# --- 10. Run configure.sh ---------------------------------------------------
log "Running configure.sh..."
chmod +x /opt/podplane/bin/configure.sh
/opt/podplane/bin/configure.sh
{{if eq .Provider.Kind "local"}}
# --- Local provider VM preparation ------------------------------------------

log "Applying local provider VM preparation..."
set +u
# shellcheck source=/dev/null
source /opt/podplane/etc/user-data.env
set -u

if ! grep -Eq "[[:space:]]oidc\.localhost([[:space:]]|$)" /etc/hosts; then
  echo "127.0.0.1 oidc.localhost" >> /etc/hosts
fi

if [ -f /etc/systemd/resolved.conf ] && ! grep -qF "DNS=1.1.1.1" /etc/systemd/resolved.conf; then
  echo "DNS=1.1.1.1" >> /etc/systemd/resolved.conf
  systemctl restart systemd-resolved
fi

if [ -n "${INSTANCE_NETDEV:-}" ] && ! ip -6 addr show dev "$INSTANCE_NETDEV" | grep -q 'fd00::1/64'; then
  ip -6 addr add fd00::1/64 dev "$INSTANCE_NETDEV"
fi

cat >/etc/systemd/system/podplane-local-https-forward.service <<'LOCAL_HTTPS_FORWARD_SERVICE'
[Unit]
Description=Podplane local HTTPS server forwarder
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/bin/socat TCP-LISTEN:{{.Local.VMForwardPortToLocalServerHTTPS}},fork,reuseaddr,bind=0.0.0.0 TCP:{{.Local.LocalServerHostFromVM}}:{{.Local.LocalServerHTTPSPort}}
Restart=always
RestartSec=2s

[Install]
WantedBy=multi-user.target
LOCAL_HTTPS_FORWARD_SERVICE
systemctl daemon-reload
systemctl enable --now podplane-local-https-forward.service

systemctl disable unattended-upgrades || true
{{end}}
# --- 11. Restart services ---------------------------------------------------
log "Running restart.sh..."
chmod +x /opt/podplane/bin/restart.sh
/opt/podplane/bin/restart.sh

# ----------------------------------------------------------------------------
log "Podplane cloud-init user-data script has completed successfully."
