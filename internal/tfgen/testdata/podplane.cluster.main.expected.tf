terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source = "hashicorp/aws"
      version = ">= 6.0"
    }
    podplane = {
      source = "podplane/podplane"
      version = ">= 1.1.0"
    }
  }
}

provider "aws" {
  region = local.provider_region
  allowed_account_ids = [local.provider_account]
}

data "aws_caller_identity" "current" {
}

data "aws_region" "current" {
}

data "podplane_userdata" "knc_arm64" {
  manifest_json = file("${path.module}/podplane.cluster.vmconfig_knc_debian-13_arm64.json")
  deps_mirror_url = "https://deps.podplane.dev"
  provider_kind = local.provider_kind
  aws_account_id = local.aws_account_id
  immutable_ssh_authorized_keys = var.immutable_ssh_authorized_keys
  enable_ssm = var.enable_ssm
}

data "podplane_userdata" "knd_arm64" {
  manifest_json = file("${path.module}/podplane.cluster.vmconfig_knd_debian-13_arm64.json")
  deps_mirror_url = "https://deps.podplane.dev"
  provider_kind = local.provider_kind
  aws_account_id = local.aws_account_id
  immutable_ssh_authorized_keys = var.immutable_ssh_authorized_keys
  enable_ssm = var.enable_ssm
}

locals {
  aws_account_id = data.aws_caller_identity.current.account_id
  aws_region = data.aws_region.current.region
  netsy_bucket_name = "${local.cluster_id}-${local.aws_account_id}-netsy"
  registry_bucket_name = "${local.cluster_id}-${local.aws_account_id}-registry"
  mutable_env = { for key, value in {
    TELEMETRY_S3_REGION = local.aws_region
    OIDC_ISSUER = var.oidc_issuer_url
    OIDC_SIGNING_ALGS = var.oidc_signing_algs == null ? null : join(",", var.oidc_signing_algs)
    KUBE_API_PUBLIC_HOSTNAME = var.kubernetes_api_hostname
    KUBE_SERVICE_ACCOUNT_ISSUER = "https://${var.kubernetes_api_hostname}:${var.kubernetes_api_port}"
    KUBE_CLUSTER_CIDR = join(",", var.kubernetes_cluster_cidr)
    KUBE_SERVICE_CLUSTER_IP_RANGE = join(",", var.kubernetes_service_cidr)
    KUBE_CLUSTER_DNS = "198.19.255.254,fdc6::ffff"
    NETSY_BUCKET = aws_s3_bucket.podplane_cluster["netsy"].bucket
    NETSY_REGION = local.aws_region
    NETSY_ASSUME_ROLE = aws_iam_role.podplane_cluster["netsy"].arn
    REGISTRY_BUCKET = aws_s3_bucket.podplane_cluster["registry"].bucket
    REGISTRY_REGION = local.aws_region
    REGISTRY_ASSUME_ROLE = aws_iam_role.podplane_cluster["registry-read-only"].arn
    SSH_AUTHORIZED_KEYS = var.ssh_authorized_keys
    TELEMETRY_ENABLED = var.telemetry_enabled == null ? null : tostring(var.telemetry_enabled)
    TELEMETRY_LOG_CLOUDINIT = var.telemetry_log_cloudinit == null ? null : tostring(var.telemetry_log_cloudinit)
    TELEMETRY_LOG_SERVICES = var.telemetry_log_services
    TELEMETRY_OTLP_ENDPOINT = var.telemetry_otlp_endpoint
    TELEMETRY_S3_BUCKET = var.telemetry_s3_bucket
    TELEMETRY_S3_ENDPOINT = var.telemetry_s3_endpoint
    TELEMETRY_S3_ASSUME_ROLE = var.telemetry_s3_assume_role
    TELEMETRY_S3_ACCESS_KEY_ID = var.telemetry_s3_access_key_id
    TELEMETRY_S3_SECRET_ACCESS_KEY = var.telemetry_s3_secret_access_key
    OIDC_CA_CERT = var.oidc_ca_cert
    KUBE_API_ETCD_SERVERS = var.kube_api_etcd_servers
    KUBE_LOG_LEVEL = var.kube_log_level == null ? null : tostring(var.kube_log_level)
    KUBE_NODE_CIDR_MASK_SIZE_IPV4 = var.kubernetes_node_cidr_mask_size_ipv4 == null ? null : tostring(var.kubernetes_node_cidr_mask_size_ipv4)
    KUBE_NODE_CIDR_MASK_SIZE_IPV6 = var.kubernetes_node_cidr_mask_size_ipv6 == null ? null : tostring(var.kubernetes_node_cidr_mask_size_ipv6)
    AWS_S3_USE_PATH_STYLE = var.aws_s3_use_path_style
    NETSY_ENDPOINT = var.netsy_endpoint
    NETSY_ACCESS_KEY_ID = var.netsy_access_key_id
    NETSY_SECRET_ACCESS_KEY = var.netsy_secret_access_key
    REGISTRY_ENABLED = var.registry_enabled == null ? null : tostring(var.registry_enabled)
    REGISTRY_ENDPOINT = var.registry_endpoint
    REGISTRY_ACCESS_KEY_ID = var.registry_access_key_id
    REGISTRY_SECRET_ACCESS_KEY = var.registry_secret_access_key
    REGISTRY_HOSTNAME = var.registry_hostname
  } : key => value if value != null }
  certificates = {
    "containerd.client" = { kind = "client", cn = "containerd.client", dns = ["{{ .Instance.Hostname }}", "localhost"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"], ttl = 8760 }
    "front-proxy.client" = { kind = "client", cn = "front-proxy-client", dns = ["{{ .Instance.Hostname }}", "localhost"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"], ttl = 8760 }
    "kube-apiserver.client" = { kind = "client", cn = "kube-apiserver.client", dns = ["{{ .Instance.Hostname }}", "localhost"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"], ttl = 8760, organization = ["system:masters"], uri = ["netsy://{{ .Cluster.ID }}/client/kube-apiserver"] }
    "kube-apiserver.server" = { kind = "server", cn = "kube-apiserver.server", dns = ["{{ .Instance.Hostname }}", "localhost", "kube-apiserver.podplane.internal", var.kubernetes_api_hostname], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1", "198.18.0.1", "fdc6::1"], ttl = 8760 }
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
      userdata = { source = "inline", encoding = "base64", content = base64encode(data.podplane_userdata.knc_arm64.content) }
      files = { "mutable.env" = { kind = "env", template = local.mutable_env }, "containerd.client.crt" = { kind = "certificate", template = "containerd.client", key = { source = "agent", name = "containerd.client" } }, "kube2iam.client.crt" = { kind = "certificate", template = "kube2iam.client", key = { source = "agent", name = "kube2iam.client" } }, "kubelet.client.crt" = { kind = "certificate", template = "kubelet.client", key = { source = "agent", name = "kubelet.client" } }, "kubelet.server.crt" = { kind = "certificate", template = "kubelet.server", key = { source = "agent", name = "kubelet.server" } }, "registry.server.crt" = { kind = "certificate", template = "registry.server", key = { source = "agent", name = "registry.server" } }, "front-proxy.client.crt" = { kind = "certificate", template = "front-proxy.client", key = { source = "agent", name = "front-proxy.client" } }, "kube-apiserver.client.crt" = { kind = "certificate", template = "kube-apiserver.client", key = { source = "agent", name = "kube-apiserver.client" } }, "kube-apiserver.server.crt" = { kind = "certificate", template = "kube-apiserver.server", key = { source = "agent", name = "kube-apiserver.server" } }, "kube-controller-manager.client.crt" = { kind = "certificate", template = "kube-controller-manager.client", key = { source = "agent", name = "kube-controller-manager.client" } }, "kube-scheduler.client.crt" = { kind = "certificate", template = "kube-scheduler.client", key = { source = "agent", name = "kube-scheduler.client" } }, "netsy.client.crt" = { kind = "certificate", template = "netsy.client", key = { source = "agent", name = "netsy.client" } }, "netsy.server.crt" = { kind = "certificate", template = "netsy.server", key = { source = "agent", name = "netsy.server" } } }
      args = { ImageId = "{{ .Image.debian_13_arm64 }}" }
    }
    "ingress" = {
      kind = "knd"
      arch = "arm64"
      vars = local.mutable_env
      userdata = { source = "inline", encoding = "base64", content = base64encode(data.podplane_userdata.knd_arm64.content) }
      files = { "mutable.env" = { kind = "env", template = local.mutable_env }, "containerd.client.crt" = { kind = "certificate", template = "containerd.client", key = { source = "agent", name = "containerd.client" } }, "kube2iam.client.crt" = { kind = "certificate", template = "kube2iam.client", key = { source = "agent", name = "kube2iam.client" } }, "kubelet.client.crt" = { kind = "certificate", template = "kubelet.client", key = { source = "agent", name = "kubelet.client" } }, "kubelet.server.crt" = { kind = "certificate", template = "kubelet.server", key = { source = "agent", name = "kubelet.server" } }, "registry.server.crt" = { kind = "certificate", template = "registry.server", key = { source = "agent", name = "registry.server" } } }
      args = { ImageId = "{{ .Image.debian_13_arm64 }}" }
    }
  }
}

module "cluster" {
  source = "nstance-dev/nstance/aws//modules/cluster"
  version = "~> 2.0"
  cluster_id = local.cluster_id
  name_prefix = local.name_prefix
}

module "account_123456789012_us_east_1" {
  source = "nstance-dev/nstance/aws//modules/account"
  version = "~> 2.0"
  cluster = module.cluster
  enable_ssm = var.enable_ssm
}

module "network_123456789012_us_east_1" {
  source = "nstance-dev/nstance/aws//modules/network"
  version = "~> 2.0"
  cluster = module.cluster
  enable_ssm = var.enable_ssm
  vpc_id = var.vpc_id
  vpc_cidr_ipv4 = var.vpc_cidr_ipv4
  enable_ipv6 = var.enable_ipv6
  subnets = local.subnets
  load_balancers = local.load_balancers
}

module "shard_us_east_1a" {
  source = "nstance-dev/nstance/aws//modules/shard"
  version = "~> 2.0"
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
        size = var.pool_sizes["control-plane"]
        subnet_pool = "control-plane"
        instance_type = var.pool_instance_types["control-plane"]
        vars = local.mutable_env
        load_balancers = {
          "main" = [6443]
        }
      }
      "ingress" = {
        template = "ingress"
        size = var.pool_sizes["ingress"]
        subnet_pool = "ingress"
        instance_type = var.pool_instance_types["ingress"]
        vars = local.mutable_env
        load_balancers = {
          "main" = [443]
        }
      }
    }
  }
}

resource "podplane_netsy_seed_s3" "cluster" {
  cluster_config_path = "${path.module}/podplane.cluster.jsonc"
  bucket = aws_s3_bucket.podplane_cluster["netsy"].bucket
  region = local.aws_region
}
