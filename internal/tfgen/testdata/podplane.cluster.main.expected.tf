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

data "podplane_userdata" "knc_arm64" {
  manifest_json = file("${path.module}/podplane.cluster.vmconfig_knc_debian-13_arm64.json")
  deps_mirror_url = "https://deps.podplane.dev"
  provider_kind = "aws"
  aws_account_id = local.aws_account_id
  immutable_ssh_authorized_keys = var.immutable_ssh_authorized_keys
  enable_ssm = var.enable_ssm
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
    SSH_AUTHORIZED_KEYS = var.ssh_authorized_keys
    TELEMETRY_ENABLED = tostring(var.telemetry_enabled)
    TELEMETRY_LOG_CLOUDINIT = tostring(var.telemetry_log_cloudinit)
    TELEMETRY_LOG_SERVICES = var.telemetry_log_services
    TELEMETRY_OTLP_ENDPOINT = var.telemetry_otlp_endpoint
    TELEMETRY_S3_BUCKET = var.telemetry_s3_bucket
    TELEMETRY_S3_REGION = local.aws_region
    TELEMETRY_S3_ENDPOINT = var.telemetry_s3_endpoint
    TELEMETRY_S3_ASSUME_ROLE = var.telemetry_s3_assume_role
    TELEMETRY_S3_ACCESS_KEY_ID = var.telemetry_s3_access_key_id
    TELEMETRY_S3_SECRET_ACCESS_KEY = var.telemetry_s3_secret_access_key
    OIDC_ISSUER = local.oidc_issuer_url
    OIDC_CA_CERT = var.oidc_ca_cert
    KUBE_API_ETCD_SERVERS = var.kube_api_etcd_servers
    KUBE_API_PUBLIC_HOSTNAME = local.kubernetes_api_hostname
    KUBE_API_INTERNAL_LB_HOSTNAME = ""
    KUBE_API_PORT = tostring(local.kubernetes_api_port)
    KUBE_LOG_LEVEL = tostring(var.kube_log_level)
    AWS_S3_USE_PATH_STYLE = var.aws_s3_use_path_style
    NETSY_BUCKET = aws_s3_bucket.podplane_cluster["netsy"].bucket
    NETSY_REGION = local.aws_region
    NETSY_ENDPOINT = var.netsy_endpoint
    NETSY_ASSUME_ROLE = aws_iam_role.podplane_cluster["netsy"].arn
    NETSY_ACCESS_KEY_ID = var.netsy_access_key_id
    NETSY_SECRET_ACCESS_KEY = var.netsy_secret_access_key
    REGISTRY_ENABLED = tostring(var.registry_enabled)
    REGISTRY_BUCKET = aws_s3_bucket.podplane_cluster["registry"].bucket
    REGISTRY_REGION = local.aws_region
    REGISTRY_ENDPOINT = var.registry_endpoint
    REGISTRY_ASSUME_ROLE = aws_iam_role.podplane_cluster["registry-read-only"].arn
    REGISTRY_ACCESS_KEY_ID = var.registry_access_key_id
    REGISTRY_SECRET_ACCESS_KEY = var.registry_secret_access_key
    REGISTRY_HOSTNAME = var.registry_hostname
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
      userdata = { source = "inline", encoding = "base64", content = base64encode(data.podplane_userdata.knc_arm64.content) }
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
