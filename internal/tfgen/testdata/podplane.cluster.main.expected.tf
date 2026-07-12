terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source = "hashicorp/aws"
      version = ">= 6.0"
    }
    podplane = {
      source = "podplane/podplane"
      version = ">= 1.0.0"
    }
  }
}

provider "aws" {
  region = "us-east-1"
  allowed_account_ids = ["123456789012"]
}

data "aws_caller_identity" "current" {
}

data "aws_region" "current" {
}

locals {
  cluster_name = "Test Cluster"
  cluster_id = "test-cluster"
  name_prefix = "test-cluster"
  aws_account_id = data.aws_caller_identity.current.account_id
  aws_region = data.aws_region.current.region
  netsy_bucket_name = "${local.cluster_id}-${local.aws_account_id}-netsy"
  registry_bucket_name = "${local.cluster_id}-${local.aws_account_id}-registry"
  oidc_issuer_url = "https://auth.example.com"
  oidc_client_id = "test-cluster"
  oidc_username_claim = "email"
  oidc_groups_claim = "groups"
  kubernetes_api_hostname = "test-cluster.k8s.local"
  kubernetes_api_port = 6443
  kubernetes_cluster_cidr = []
  kubernetes_service_cidr = []
  mutable_env = {
    SSH_AUTHORIZED_KEY = var.ssh_authorized_key
    KUBE_API_PUBLIC_HOSTNAME = local.kubernetes_api_hostname
    KUBE_API_PORT = tostring(local.kubernetes_api_port)
    KUBE_API_INTERNAL_LB_HOSTNAME = ""
    KUBE_API_ETCD_SERVERS = var.kube_api_etcd_servers
    OIDC_ISSUER = local.oidc_issuer_url
    OIDC_CA_CERT = var.oidc_ca_cert
    KUBE_LOG_LEVEL = tostring(var.kube_log_level)
  
    NETSY_BUCKET = aws_s3_bucket.netsy.bucket
    NETSY_ENDPOINT = var.netsy_endpoint
    NETSY_ASSUME_ROLE = aws_iam_role.netsy.arn
    NETSY_REGION = local.aws_region
    NETSY_ACCESS_KEY_ID = var.netsy_access_key_id
    NETSY_SECRET_ACCESS_KEY = var.netsy_secret_access_key
  
    TELEMETRY_ENABLED = tostring(var.telemetry_enabled)
    TELEMETRY_LOG_SERVICES = var.telemetry_log_services
    TELEMETRY_LOG_CLOUDINIT = tostring(var.telemetry_log_cloudinit)
    TELEMETRY_S3_BUCKET = var.telemetry_s3_bucket
    TELEMETRY_S3_ENDPOINT = var.telemetry_s3_endpoint
    TELEMETRY_S3_REGION = local.aws_region
    TELEMETRY_S3_ASSUME_ROLE = var.telemetry_s3_assume_role
    TELEMETRY_S3_ACCESS_KEY_ID = var.telemetry_s3_access_key_id
    TELEMETRY_S3_SECRET_ACCESS_KEY = var.telemetry_s3_secret_access_key
    TELEMETRY_OTLP_ENDPOINT = var.telemetry_otlp_endpoint
  
    REGISTRY_ENABLED = tostring(var.registry_enabled)
    REGISTRY_BUCKET = aws_s3_bucket.registry.bucket
    REGISTRY_HOSTNAME = var.registry_hostname
    REGISTRY_ENDPOINT = var.registry_endpoint
    REGISTRY_REGION = local.aws_region
    REGISTRY_ASSUME_ROLE = aws_iam_role.registry_read_only.arn
    REGISTRY_ACCESS_KEY_ID = var.registry_access_key_id
    REGISTRY_SECRET_ACCESS_KEY = var.registry_secret_access_key
    AWS_S3_USE_PATH_STYLE = var.aws_s3_use_path_style
  }
  userdata = {
    "control-plane" = <<-USERDATA
    #!/bin/bash -e
    # Podplane VM userdata (rendered).
    # Provider: aws
    # Cluster ID: {{ .Cluster.ID }}
    # Instance ID: {{ .Instance.ID }}
    # Manifest version: 2026.01.01
    # OS: debian-13
    # Arch: arm64
    # Deps Mirror=https://deps.podplane.dev
    set -euo pipefail
    export DEBIAN_FRONTEND=noninteractive

    log() {
      printf '[userdata] %s\tts=%s\n' "$*" "$EPOCHREALTIME"
    }

    log "Podplane cloud-init user-data script has started."

    # ----------------------------------------------------------------------------

    # --- 1. Configure hostname --------------------------------------------------

    hostnamectl set-hostname {{ .Instance.ID }}


    # --- 2. Bootstrap provider-specific tools -----------------------------------


    %{ if var.enable_ssm ~}
    log "Ensuring AWS SSM Agent is installed and running..."
    if command -v snap >/dev/null 2>&1 && snap list amazon-ssm-agent >/dev/null 2>&1; then
      snap start amazon-ssm-agent
    elif dpkg -s amazon-ssm-agent >/dev/null 2>&1; then
      systemctl enable --now amazon-ssm-agent
    else
      curl -fsSL -o /tmp/amazon-ssm-agent.deb \
        "https://s3.amazonaws.com/ec2-downloads-windows/SSMAgent/latest/debian_arm64/amazon-ssm-agent.deb"
      dpkg -i /tmp/amazon-ssm-agent.deb
      rm -f /tmp/amazon-ssm-agent.deb
      systemctl enable --now amazon-ssm-agent
    fi
    %{ endif ~}


    # --- 3. Check connectivity to Nstance Server --------------------------------

    REGISTRATION_ADDR="{{ .Server.RegistrationAddr }}"
    log "Checking connectivity to nstance-server at $REGISTRATION_ADDR..."
    attempt=0
    while true
    do
      attempt=$((attempt + 1))
      if timeout 5 bash -c "echo > /dev/tcp/$${REGISTRATION_ADDR%:*}/$${REGISTRATION_ADDR##*:}" 2>/dev/null
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

    # --- 4. Download and verify dependencies ------------------------------------

    ARTIFACTS_DIR="/opt/podplane/artifacts"
    mkdir -p "$ARTIFACTS_DIR"

    log "Downloading 2 dependencies..."
    curl -sfL --parallel --parallel-max 10 --parallel-immediate \
      -o "$${ARTIFACTS_DIR}/runc" "https://deps.podplane.dev/vmconfig/artifacts/runc/1.2.3/runc" \
      -o "$${ARTIFACTS_DIR}/vmconfig.tar.gz" "https://deps.podplane.dev/vmconfig/artifacts/vmconfig/2026.01.01/vmconfig.tar.gz" \
      >/dev/null

    log "Verifying checksums..."
    while read -r digest filename; do
      case "$digest" in
        sha256:*) echo "$${digest#sha256:}  $${filename}" | sha256sum -c --quiet ;;
        sha512:*) echo "$${digest#sha512:}  $${filename}" | sha512sum -c --quiet ;;
        *) echo "Unsupported digest algorithm for $${filename}: $${digest}" >&2; exit 1 ;;
      esac
    done <<CHECKSUMS
    sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  $${ARTIFACTS_DIR}/runc
    sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  $${ARTIFACTS_DIR}/vmconfig.tar.gz
    CHECKSUMS

    # --- 5. Extract vmconfig tarball --------------------------------------------

    log "Extracting vmconfig.tar.gz..."
    tar -xzf "$${ARTIFACTS_DIR}/vmconfig.tar.gz" -C /


    # --- 6. Write user-data environment file ------------------------------------

    log "Writing user-data.env file..."
    mkdir -p /opt/podplane/etc
    cat > /opt/podplane/etc/user-data.env <<'USERDATA_ENV'
    SSH_AUTHORIZED_KEY='{{ .Vars.SSH_AUTHORIZED_KEY }}'

    INSTANCE_ID='{{ .Instance.ID }}'
    CLUSTER_ID='{{ .Cluster.ID }}'

    PROVIDER_KIND='aws'
    PROVIDER_REGION='{{ .Provider.Region }}'
    PROVIDER_ZONE='{{ .Provider.Zone }}'
    PROVIDER_INSTANCE_TYPE='{{ .Instance.Type }}'
    AWS_ACCOUNT_ID='${local.aws_account_id}'
    GOOGLE_PROJECT_ID=''

    NSTANCE_CA_CERT='{{ .Cluster.CACert }}'
    NSTANCE_SERVER_REGISTRATION_ADDR='{{ .Server.RegistrationAddr }}'
    NSTANCE_SERVER_AGENT_ADDR='{{ .Server.AgentAddr }}'
    USERDATA_ENV
    chmod 0600 /opt/podplane/etc/user-data.env

    # --- 7. Write sensitive nstance bootstrap files -----------------------------
    log "Writing nstance registration nonce file..."
    mkdir -p /opt/nstance-agent/identity
    cat > /opt/nstance-agent/identity/nonce.jwt <<'NSTANCE_NONCE_JWT'
    {{ .Nonce }}
    NSTANCE_NONCE_JWT
    cat > /opt/nstance-agent/identity/ca.crt <<'NSTANCE_CA_CERT'
    {{ .Cluster.CACert }}
    NSTANCE_CA_CERT
    chmod 0600 /opt/nstance-agent/identity/nonce.jwt /opt/nstance-agent/identity/ca.crt


    # -- 8. Run install.sh -------------------------------------------------------



    log "Running install.sh..."
    chmod +x /opt/podplane/bin/install.sh
    /opt/podplane/bin/install.sh

    # --- 9. Run configure.sh ----------------------------------------------------
    log "Running configure.sh..."
    chmod +x /opt/podplane/bin/configure.sh
    /opt/podplane/bin/configure.sh

    # --- 10. Restart services ---------------------------------------------------
    log "Running restart.sh..."
    chmod +x /opt/podplane/bin/restart.sh
    /opt/podplane/bin/restart.sh

    # ----------------------------------------------------------------------------
    log "Podplane cloud-init user-data script has completed successfully."
    USERDATA
  }
  certificates = {
    "containerd.client" = { kind = "client", cn = "containerd.client", dns = ["{{ .Instance.Hostname }}", "localhost"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"], ttl = 8760 }
    "front-proxy.client" = { kind = "client", cn = "front-proxy-client", dns = ["{{ .Instance.Hostname }}", "localhost"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"], ttl = 8760 }
    "kube-apiserver.client" = { kind = "client", cn = "kube-apiserver.client", dns = ["{{ .Instance.Hostname }}", "localhost"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"], ttl = 8760, organization = ["system:masters"], uri = ["netsy://{{ .Cluster.ID }}/client/kube-apiserver"] }
    "kube-apiserver.server" = { kind = "server", cn = "kube-apiserver.server", dns = ["{{ .Instance.Hostname }}", "localhost", "kube-apiserver.podplane.internal"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1", "198.18.0.1", "fdc6::1"], ttl = 8760 }
    "kube-controller-manager.client" = { kind = "client", cn = "system:kube-controller-manager", dns = ["{{ .Instance.Hostname }}", "localhost"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"], ttl = 8760 }
    "kube-scheduler.client" = { kind = "client", cn = "system:kube-scheduler", dns = ["{{ .Instance.Hostname }}", "localhost"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"], ttl = 8760 }
    "kube2iam.client" = { kind = "client", cn = "kube2iam.client", dns = ["{{ .Instance.Hostname }}", "localhost"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"], ttl = 8760 }
    "kubelet.client" = { kind = "client", cn = "system:node:{{ .Instance.ID }}", dns = ["{{ .Instance.Hostname }}", "localhost"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"], ttl = 8760, organization = ["system:nodes"] }
    "kubelet.server" = { kind = "server", cn = "kubelet.server", dns = ["{{ .Instance.Hostname }}", "localhost"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"], ttl = 8760 }
    "netsy.client" = { kind = "client", cn = "netsy.client", dns = ["{{ .Instance.Hostname }}", "localhost"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"], ttl = 8760, uri = ["netsy://{{ .Cluster.ID }}/peer/{{ .Instance.ID }}"] }
    "netsy.server" = { kind = "server", cn = "netsy.server", dns = ["{{ .Instance.Hostname }}", "localhost"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"], ttl = 8760, uri = ["netsy://{{ .Cluster.ID }}/peer/{{ .Instance.ID }}"] }
    "registry.server" = { kind = "server", cn = "registry.server", dns = ["{{ .Instance.Hostname }}", "localhost"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"], ttl = 8760 }
  }
  templates = {
    "control-plane" = {
      kind = "knc"
      arch = "arm64"
      vars = local.mutable_env
      userdata = { source = "inline", encoding = "base64", content = base64encode(local.userdata["control-plane"]) }
      files = { "mutable.env" = { kind = "env", template = local.mutable_env }, "containerd.client.crt" = { kind = "certificate", template = "containerd.client", key = { source = "agent", name = "containerd.client" } }, "kube2iam.client.crt" = { kind = "certificate", template = "kube2iam.client", key = { source = "agent", name = "kube2iam.client" } }, "kubelet.client.crt" = { kind = "certificate", template = "kubelet.client", key = { source = "agent", name = "kubelet.client" } }, "kubelet.server.crt" = { kind = "certificate", template = "kubelet.server", key = { source = "agent", name = "kubelet.server" } }, "registry.server.crt" = { kind = "certificate", template = "registry.server", key = { source = "agent", name = "registry.server" } }, "front-proxy.client.crt" = { kind = "certificate", template = "front-proxy.client", key = { source = "agent", name = "front-proxy.client" } }, "kube-apiserver.client.crt" = { kind = "certificate", template = "kube-apiserver.client", key = { source = "agent", name = "kube-apiserver.client" } }, "kube-apiserver.server.crt" = { kind = "certificate", template = "kube-apiserver.server", key = { source = "agent", name = "kube-apiserver.server" } }, "kube-controller-manager.client.crt" = { kind = "certificate", template = "kube-controller-manager.client", key = { source = "agent", name = "kube-controller-manager.client" } }, "kube-scheduler.client.crt" = { kind = "certificate", template = "kube-scheduler.client", key = { source = "agent", name = "kube-scheduler.client" } }, "netsy.client.crt" = { kind = "certificate", template = "netsy.client", key = { source = "agent", name = "netsy.client" } }, "netsy.server.crt" = { kind = "certificate", template = "netsy.server", key = { source = "agent", name = "netsy.server" } } }
      args = { ImageId = "{{ .Image.debian_13_arm64 }}" }
    }
  }
}

module "cluster" {
  source = "nstance-dev/nstance/aws//modules/cluster"
  cluster_id = local.cluster_id
  name_prefix = local.name_prefix
}

module "account_123456789012_us_east_1" {
  source = "nstance-dev/nstance/aws//modules/account"
  cluster = module.cluster
  enable_ssm = var.enable_ssm
}

resource "aws_s3_bucket" "netsy" {
  bucket = local.netsy_bucket_name
}

resource "aws_s3_bucket_public_access_block" "netsy" {
  bucket = aws_s3_bucket.netsy.id
  block_public_acls = true
  block_public_policy = true
  ignore_public_acls = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "netsy" {
  bucket = aws_s3_bucket.netsy.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket" "registry" {
  bucket = local.registry_bucket_name
}

resource "aws_s3_bucket_public_access_block" "registry" {
  bucket = aws_s3_bucket.registry.id
  block_public_acls = true
  block_public_policy = true
  ignore_public_acls = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "registry" {
  bucket = aws_s3_bucket.registry.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

data "aws_iam_policy_document" "assume_from_knc" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      type = "AWS"
      identifiers = [module.account_123456789012_us_east_1.agent_iam_role_arn]
    }
  }
}

resource "aws_iam_role" "netsy" {
  name = "${local.name_prefix}-netsy"
  assume_role_policy = data.aws_iam_policy_document.assume_from_knc.json
}

resource "aws_iam_role_policy" "netsy" {
  name = "${local.name_prefix}-netsy-policy"
  role = aws_iam_role.netsy.id
  policy = data.aws_iam_policy_document.netsy.json
}

resource "aws_iam_role" "registry_read_only" {
  name = "${local.name_prefix}-registry-read-only"
  assume_role_policy = data.aws_iam_policy_document.assume_from_knc.json
}

resource "aws_iam_role_policy" "registry_read_only" {
  name = "${local.name_prefix}-registry-read-only-policy"
  role = aws_iam_role.registry_read_only.id
  policy = data.aws_iam_policy_document.registry_read_only.json
}

resource "aws_iam_role" "registry_read_write" {
  name = "${local.name_prefix}-registry-read-write"
  assume_role_policy = data.aws_iam_policy_document.assume_from_knc.json
}

resource "aws_iam_role_policy" "registry_read_write" {
  name = "${local.name_prefix}-registry-read-write-policy"
  role = aws_iam_role.registry_read_write.id
  policy = data.aws_iam_policy_document.registry_read_write.json
}

data "aws_iam_policy_document" "podplane_knc" {
  statement {
    sid = "AssumePodplaneWorkloadRoles"
    actions = ["sts:AssumeRole"]
    resources = [aws_iam_role.netsy.arn, aws_iam_role.registry_read_only.arn, aws_iam_role.registry_read_write.arn]
  }

  statement {
    sid = "DescribeRegions"
    actions = ["ec2:DescribeRegions"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "podplane_knc" {
  name = "${local.name_prefix}-podplane-knc-policy"
  role = module.account_123456789012_us_east_1.agent_iam_role_name
  policy = data.aws_iam_policy_document.podplane_knc.json
}

data "aws_iam_policy_document" "netsy" {
  statement {
    sid = "NetsyS3ObjectOperations"
    actions = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:GetObjectAttributes", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"]
    resources = ["${aws_s3_bucket.netsy.arn}/*"]
  }

  statement {
    sid = "NetsyS3BucketOperations"
    actions = ["s3:ListBucket", "s3:ListBucketMultipartUploads"]
    resources = [aws_s3_bucket.netsy.arn]
  }
}

data "aws_iam_policy_document" "registry_read_only" {
  statement {
    actions = ["s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketMultipartUploads"]
    resources = [aws_s3_bucket.registry.arn]
  }

  statement {
    actions = ["s3:GetObject", "s3:ListMultipartUploadParts"]
    resources = ["${aws_s3_bucket.registry.arn}/*"]
  }
}

data "aws_iam_policy_document" "registry_read_write" {
  statement {
    actions = ["s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketMultipartUploads"]
    resources = [aws_s3_bucket.registry.arn]
  }

  statement {
    actions = ["s3:GetObject", "s3:ListMultipartUploadParts", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload"]
    resources = ["${aws_s3_bucket.registry.arn}/*"]
  }
}

module "network_123456789012_us_east_1" {
  source = "nstance-dev/nstance/aws//modules/network"
  cluster = module.cluster
  enable_ssm = var.enable_ssm
  vpc_cidr_ipv4 = "172.18.0.0/16"
  enable_ipv6 = true
  subnets = {
    "control-plane" = {
      "us-east-1a" = [{ ipv4_cidr = "172.18.1.0/24", nat_subnet = "public" }]
    }
    "nstance" = {
      "us-east-1a" = [{ ipv4_cidr = "172.18.20.0/28", nat_subnet = "public" }]
    }
    "public" = {
      "us-east-1a" = [{ ipv4_cidr = "172.18.10.0/28", public = true, nat_gateway = true }]
    }
  }
  load_balancers = {
    "public-control-plane" = { ports = [6443], subnets = "public", public = true }
  }
}

module "shard_us_east_1a" {
  source = "nstance-dev/nstance/aws//modules/shard"
  cluster = module.cluster
  account = module.account_123456789012_us_east_1
  network = module.network_123456789012_us_east_1
  shard = "us-east-1a"
  zone = "us-east-1a"
  enable_ssm = var.enable_ssm
  certificates = local.certificates
  templates = local.templates
  groups = {
    "default" = {
      "control-plane" = {
        template = "control-plane"
        size = 1
        subnet_pool = "control-plane"
        instance_type = "t4g.medium"
        vars = local.mutable_env
        load_balancers = ["public-control-plane"]
      }
    }
  }
}

resource "podplane_netsy_seed_s3" "cluster" {
  cluster_config_path = "${path.module}/podplane.cluster.jsonc"
  bucket = aws_s3_bucket.netsy.bucket
  region = local.aws_region
  depends_on = [aws_s3_bucket.netsy]
}
