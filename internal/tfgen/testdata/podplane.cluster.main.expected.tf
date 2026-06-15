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
    NSTANCE_SERVER_REGISTRATION_ADDR = "{{ .Server.RegistrationAddr }}"
    NSTANCE_SERVER_AGENT_ADDR = "{{ .Server.AgentAddr }}"
    KUBE_API_ETCD_SERVERS = var.kube_api_etcd_servers
    OIDC_ISSUER = local.oidc_issuer_url
    OIDC_CUSTOM_CA = var.oidc_custom_ca
    OIDC_CA_FILE = var.oidc_ca_file
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
  templates = {
    "control-plane" = {
      kind = "knc"
      arch = "arm64"
      vars = local.mutable_env
      userdata = { source = "inline", encoding = "base64", content = base64encode("#!/bin/bash -e\n# Podplane VM userdata (rendered).\n# Provider: aws\n# Cluster ID: {{ .Cluster.ID }}\n# Instance ID: {{ .Instance.ID }}\n# Manifest version: 2026.01.01\n# OS: debian-13\n# Arch: arm64\n# Cluster bucket names (cluster-prefixed):\n#   netsy={{ .Vars.NETSY_BUCKET }}\n#   registry={{ .Vars.REGISTRY_BUCKET }}\n#   telemetry={{ .Vars.TELEMETRY_S3_BUCKET }}\n# OIDC Issuer: {{ .Vars.OIDC_ISSUER }}\n# Deps Mirror=https://deps.podplane.dev\nset -euo pipefail\nexport DEBIAN_FRONTEND=noninteractive\n\nlog() {\n  printf '[userdata] %s\\tts=%s\\n' \"$*\" \"$EPOCHREALTIME\"\n}\n\nlog \"Podplane cloud-init user-data script has started.\"\n\n# ----------------------------------------------------------------------------\n\n# --- 1. Configure hostname --------------------------------------------------\n\nhostnamectl set-hostname {{ .Instance.ID }}\n\n\n# --- 2. Bootstrap provider-specific tools -----------------------------------\n\n\n%{ if var.enable_ssm ~}\nlog \"Ensuring AWS SSM Agent is installed and running...\"\nif command -v snap >/dev/null 2>&1 && snap list amazon-ssm-agent >/dev/null 2>&1; then\n  snap start amazon-ssm-agent\nelif dpkg -s amazon-ssm-agent >/dev/null 2>&1; then\n  systemctl enable --now amazon-ssm-agent\nelse\n  curl -fsSL -o /tmp/amazon-ssm-agent.deb \\\n    \"https://s3.amazonaws.com/ec2-downloads-windows/SSMAgent/latest/debian_arm64/amazon-ssm-agent.deb\"\n  dpkg -i /tmp/amazon-ssm-agent.deb\n  rm -f /tmp/amazon-ssm-agent.deb\n  systemctl enable --now amazon-ssm-agent\nfi\n%{ endif ~}\n\n\n# --- 3. Check connectivity to Nstance Server --------------------------------\n\nREGISTRATION_ADDR=\"{{ .Server.RegistrationAddr }}\"\nlog \"Checking connectivity to nstance-server at $REGISTRATION_ADDR...\"\nattempt=0\nwhile true\ndo\n  attempt=$((attempt + 1))\n  if timeout 5 bash -c \"echo > /dev/tcp/$${REGISTRATION_ADDR%:*}/$${REGISTRATION_ADDR##*:}\" 2>/dev/null\n  then\n    log \"Connection successful!\"\n    break\n  fi\n  retry_in=15\n  if [ $attempt -lt 3 ]\n  then\n    retry_in=3\n  fi\n  log \"Failed to connect to nstance-server at $REGISTRATION_ADDR (attempt $attempt), retrying in $retry_in seconds...\"\n  sleep $retry_in\ndone\n\n# --- 4. Download and verify dependencies ------------------------------------\n\nARTIFACTS_DIR=\"/opt/podplane/artifacts\"\nmkdir -p \"$ARTIFACTS_DIR\"\n\nlog \"Downloading 2 dependencies...\"\ncurl -sfL --parallel --parallel-max 10 --parallel-immediate \\\n  -o \"$${ARTIFACTS_DIR}/runc\" \"https://deps.podplane.dev/vmconfig/artifacts/runc/1.2.3/runc\" \\\n  -o \"$${ARTIFACTS_DIR}/vmconfig.tar.gz\" \"https://deps.podplane.dev/vmconfig/artifacts/vmconfig/2026.01.01/vmconfig.tar.gz\" \\\n  >/dev/null\n\nlog \"Verifying checksums...\"\nwhile read -r digest filename; do\n  case \"$digest\" in\n    sha256:*) echo \"$${digest#sha256:}  $${filename}\" | sha256sum -c --quiet ;;\n    sha512:*) echo \"$${digest#sha512:}  $${filename}\" | sha512sum -c --quiet ;;\n    *) echo \"Unsupported digest algorithm for $${filename}: $${digest}\" >&2; exit 1 ;;\n  esac\ndone <<CHECKSUMS\nsha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  $${ARTIFACTS_DIR}/runc\nsha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  $${ARTIFACTS_DIR}/vmconfig.tar.gz\nCHECKSUMS\n\n# --- 5. Extract vmconfig tarball --------------------------------------------\n\nlog \"Extracting vmconfig.tar.gz...\"\ntar -xzf \"$${ARTIFACTS_DIR}/vmconfig.tar.gz\" -C /\n\n\n# --- 6. Write user-data environment file ------------------------------------\n\nlog \"Writing user-data.env file...\"\nmkdir -p /opt/podplane/etc\ncat > /opt/podplane/etc/user-data.env <<'USERDATA_ENV'\nSSH_AUTHORIZED_KEY='{{ .Vars.SSH_AUTHORIZED_KEY }}'\n\nINSTANCE_ID='{{ .Instance.ID }}'\nCLUSTER_ID='{{ .Cluster.ID }}'\n\nPROVIDER_KIND='aws'\nPROVIDER_REGION='{{ .Provider.Region }}'\nPROVIDER_ZONE='{{ .Provider.Zone }}'\nPROVIDER_INSTANCE_TYPE='{{ .Instance.Type }}'\nAWS_ACCOUNT_ID='${local.aws_account_id}'\nGOOGLE_PROJECT_ID=''\n\nOIDC_ISSUER='{{ .Vars.OIDC_ISSUER }}'\nOIDC_CUSTOM_CA='{{ .Vars.OIDC_CUSTOM_CA }}'\nOIDC_CA_FILE='{{ .Vars.OIDC_CA_FILE }}'\n\nKUBE_LOG_LEVEL='{{ .Vars.KUBE_LOG_LEVEL }}'\nKUBE_API_PUBLIC_HOSTNAME='{{ .Vars.KUBE_API_PUBLIC_HOSTNAME }}'\nKUBE_API_PORT='{{ .Vars.KUBE_API_PORT }}'\nKUBE_API_INTERNAL_LB_HOSTNAME='{{ .Vars.KUBE_API_INTERNAL_LB_HOSTNAME }}'\nKUBE_API_ETCD_SERVERS='{{ .Vars.KUBE_API_ETCD_SERVERS }}'\n\nNSTANCE_CA_CERT='{{ .Cluster.CACert }}'\nNSTANCE_SERVER_REGISTRATION_ADDR='{{ .Server.RegistrationAddr }}'\nNSTANCE_SERVER_AGENT_ADDR='{{ .Server.AgentAddr }}'\n\nNETSY_BUCKET='{{ .Vars.NETSY_BUCKET }}'\nNETSY_ENDPOINT='{{ .Vars.NETSY_ENDPOINT }}'\nNETSY_REGION='{{ .Vars.NETSY_REGION }}'\nNETSY_ASSUME_ROLE='{{ .Vars.NETSY_ASSUME_ROLE }}'\nNETSY_ACCESS_KEY_ID='{{ .Vars.NETSY_ACCESS_KEY_ID }}'\nNETSY_SECRET_ACCESS_KEY='{{ .Vars.NETSY_SECRET_ACCESS_KEY }}'\n\nTELEMETRY_ENABLED='{{ .Vars.TELEMETRY_ENABLED }}'\nTELEMETRY_S3_BUCKET='{{ .Vars.TELEMETRY_S3_BUCKET }}'\nTELEMETRY_S3_ENDPOINT='{{ .Vars.TELEMETRY_S3_ENDPOINT }}'\nTELEMETRY_S3_REGION='{{ .Vars.TELEMETRY_S3_REGION }}'\nTELEMETRY_S3_ASSUME_ROLE='{{ .Vars.TELEMETRY_S3_ASSUME_ROLE }}'\nTELEMETRY_LOG_SERVICES='{{ .Vars.TELEMETRY_LOG_SERVICES }}'\nTELEMETRY_LOG_CLOUDINIT='{{ .Vars.TELEMETRY_LOG_CLOUDINIT }}'\nTELEMETRY_S3_ACCESS_KEY_ID='{{ .Vars.TELEMETRY_S3_ACCESS_KEY_ID }}'\nTELEMETRY_S3_SECRET_ACCESS_KEY='{{ .Vars.TELEMETRY_S3_SECRET_ACCESS_KEY }}'\nTELEMETRY_OTLP_ENDPOINT='{{ .Vars.TELEMETRY_OTLP_ENDPOINT }}'\n\nREGISTRY_ENABLED='{{ .Vars.REGISTRY_ENABLED }}'\nREGISTRY_BUCKET='{{ .Vars.REGISTRY_BUCKET }}'\nREGISTRY_HOSTNAME='{{ .Vars.REGISTRY_HOSTNAME }}'\nREGISTRY_ENDPOINT='{{ .Vars.REGISTRY_ENDPOINT }}'\nREGISTRY_REGION='{{ .Vars.REGISTRY_REGION }}'\nREGISTRY_ASSUME_ROLE='{{ .Vars.REGISTRY_ASSUME_ROLE }}'\nREGISTRY_ACCESS_KEY_ID='{{ .Vars.REGISTRY_ACCESS_KEY_ID }}'\nREGISTRY_SECRET_ACCESS_KEY='{{ .Vars.REGISTRY_SECRET_ACCESS_KEY }}'\nAWS_S3_USE_PATH_STYLE='{{ .Vars.AWS_S3_USE_PATH_STYLE }}'\nUSERDATA_ENV\nchmod 0600 /opt/podplane/etc/user-data.env\n\n# --- 7. Write sensitive nstance bootstrap files -----------------------------\nlog \"Writing nstance registration nonce file...\"\nmkdir -p /opt/nstance-agent/identity\ncat > /opt/nstance-agent/identity/nonce.jwt <<'NSTANCE_NONCE_JWT'\n{{ .Nonce }}\nNSTANCE_NONCE_JWT\ncat > /opt/nstance-agent/identity/ca.crt <<'NSTANCE_CA_CERT'\n{{ .Cluster.CACert }}\nNSTANCE_CA_CERT\nchmod 0600 /opt/nstance-agent/identity/nonce.jwt /opt/nstance-agent/identity/ca.crt\n\n\n# -- 8. Run install.sh -------------------------------------------------------\n\n\n\nlog \"Running install.sh...\"\nchmod +x /opt/podplane/bin/install.sh\n/opt/podplane/bin/install.sh\n\n# --- 9. Run configure.sh ----------------------------------------------------\nlog \"Running configure.sh...\"\nchmod +x /opt/podplane/bin/configure.sh\n/opt/podplane/bin/configure.sh\n\n# --- 10. Restart services ---------------------------------------------------\nlog \"Running restart.sh...\"\nchmod +x /opt/podplane/bin/restart.sh\n/opt/podplane/bin/restart.sh\n\n# ----------------------------------------------------------------------------\nlog \"Podplane cloud-init user-data script has completed successfully.\"\n") }
      files = { "mutable.env" = { kind = "env", template = local.mutable_env } }
      args = { ImageId = "{{ .Image.debian_13_arm64 }}" }
    }
  }
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
  region = "us-east-1"
  depends_on = [aws_s3_bucket.netsy]
}
