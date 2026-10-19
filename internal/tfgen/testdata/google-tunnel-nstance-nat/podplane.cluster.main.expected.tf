# Cluster providers, VM templates, and Nstance infrastructure, including bootstrap configuration.

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    google = {
      source = "hashicorp/google"
      version = ">= 6.0"
    }
    podplane = {
      source = "podplane/podplane"
      version = ">= 0.0.0"
    }
  }
}

provider "google" {
  project = local.provider_project
  region = local.provider_region
}

data "podplane_userdata" "knc_amd64" {
  manifest_json = file("${path.module}/podplane.cluster.vmconfig_knc_debian-13_amd64.json")
  deps_mirror_url = "https://deps.podplane.dev"
  provider_kind = local.provider_kind
  google_project_id = local.provider_project
  immutable_ssh_authorized_keys = var.immutable_ssh_authorized_keys
}

data "podplane_userdata" "knd_amd64" {
  manifest_json = file("${path.module}/podplane.cluster.vmconfig_knd_debian-13_amd64.json")
  deps_mirror_url = "https://deps.podplane.dev"
  provider_kind = local.provider_kind
  google_project_id = local.provider_project
  immutable_ssh_authorized_keys = var.immutable_ssh_authorized_keys
}

data "podplane_userdata" "nat_amd64" {
  manifest_json = file("${path.module}/podplane.cluster.vmconfig_nat_debian-13_amd64.json")
  deps_mirror_url = "https://deps.podplane.dev"
  provider_kind = local.provider_kind
  google_project_id = local.provider_project
  immutable_ssh_authorized_keys = var.immutable_ssh_authorized_keys
}

data "podplane_userdata" "nst_amd64_us_central1_a" {
  manifest_json = file("${path.module}/podplane.cluster.vmconfig_nst_debian-13_amd64.json")
  deps_mirror_url = "https://deps.podplane.dev"
  provider_kind = local.provider_kind
  google_project_id = local.provider_project
  immutable_ssh_authorized_keys = var.immutable_ssh_authorized_keys
  server_env = merge(local.host_env, {
    "CLUSTER_ID" = local.cluster_id
    "PROVIDER_REGION" = local.provider_region
    "PROVIDER_ZONE" = "us-central1-a"
    "NSTANCE_SERVER_STORAGE" = "gcs"
    "NSTANCE_SERVER_BUCKET" = module.cluster.bucket
    "NSTANCE_SERVER_SHARD" = "us-central1-a"
    "NSTANCE_SERVER_PREFIX" = "shard/us-central1-a/"
    "NSTANCE_SLEEP_ENABLED" = "true"
    "NSTANCE_TUNNEL_ENABLED" = "true"
    "NSTANCE_TUNNEL_STORAGE" = "gcs"
    "NSTANCE_TUNNEL_BUCKET" = module.cluster.bucket
    "NSTANCE_TUNNEL_SHARD" = "us-central1-a"
    "NSTANCE_TUNNEL_PREFIX" = "shard/us-central1-a/"
  })
}

resource "terraform_data" "deployment_modes" {
  input = {
    nat = var.nat_mode
    exposure = "tunnel"
  }

  lifecycle {
    ignore_changes = [input]

    postcondition {
      condition = self.input.nat == var.nat_mode
      error_message = "Changing NAT mode (provider <-> nstance) in place is not supported. Destroy the cluster, then recreate it with the desired mode."
    }

    postcondition {
      condition = self.input.exposure == "tunnel"
      error_message = "Changing exposure mode (NLB <-> tunnel) in place is not supported. Destroy the cluster, then recreate it with the desired mode."
    }
  }
}

locals {
  netsy_bucket_name = "${local.cluster_id}-${local.provider_project}-netsy"
  registry_bucket_name = "${local.cluster_id}-${local.provider_project}-registry"
  host_env = { for key, value in {
    SSH_AUTHORIZED_KEYS = var.ssh_authorized_keys
    TELEMETRY_ENABLED = var.telemetry_enabled == null ? null : tostring(var.telemetry_enabled)
    TELEMETRY_LOG_CLOUDINIT = var.telemetry_log_cloudinit == null ? null : tostring(var.telemetry_log_cloudinit)
    TELEMETRY_LOG_SERVICES = var.telemetry_log_services
    TELEMETRY_OTLP_ENDPOINT = var.telemetry_otlp_endpoint
    TELEMETRY_S3_BUCKET = var.telemetry_s3_bucket
    TELEMETRY_S3_REGION = local.provider_region
    TELEMETRY_S3_ENDPOINT = var.telemetry_s3_endpoint
    TELEMETRY_S3_ASSUME_ROLE = var.telemetry_s3_assume_role
    AWS_S3_USE_PATH_STYLE = var.aws_s3_use_path_style
    REGISTRY_ENABLED = var.registry_enabled == null ? null : tostring(var.registry_enabled)
    REGISTRY_BUCKET = google_storage_bucket.podplane_cluster["registry"].name
    REGISTRY_REGION = local.provider_region
    REGISTRY_ENDPOINT = var.registry_endpoint
    REGISTRY_HOSTNAME = var.registry_hostname
  } : key => value if value != null }
  mutable_env = merge(local.host_env, { for key, value in {
    NETSY_BUCKET = google_storage_bucket.podplane_cluster["netsy"].name
    NETSY_REGION = local.provider_region
    OIDC_ISSUER = var.oidc_issuer_url
    OIDC_USERNAME_CLAIM = local.oidc_username_claim
    OIDC_SIGNING_ALGS = var.oidc_signing_algs == null ? null : join(",", var.oidc_signing_algs)
    KUBE_API_PUBLIC_HOSTNAME = var.kubernetes_api_hostname
    KUBE_SERVICE_ACCOUNT_ISSUER = "https://${var.kubernetes_api_hostname}:${var.kubernetes_api_port}"
    KUBE_CLUSTER_CIDR = join(",", var.kubernetes_cluster_cidr)
    KUBE_SERVICE_CLUSTER_IP_RANGE = join(",", var.kubernetes_service_cidr)
    KUBE_CLUSTER_DNS = "198.19.255.254,fdc6::ffff"
    TELEMETRY_S3_ACCESS_KEY_ID = var.telemetry_s3_access_key_id
    TELEMETRY_S3_SECRET_ACCESS_KEY = var.telemetry_s3_secret_access_key
    OIDC_CA_CERT = var.oidc_ca_cert
    KUBE_API_ETCD_SERVERS = var.kube_api_etcd_servers
    KUBE_LOG_LEVEL = var.kube_log_level == null ? null : tostring(var.kube_log_level)
    KUBE_NODE_CIDR_MASK_SIZE_IPV4 = var.kubernetes_node_cidr_mask_size_ipv4 == null ? null : tostring(var.kubernetes_node_cidr_mask_size_ipv4)
    KUBE_NODE_CIDR_MASK_SIZE_IPV6 = var.kubernetes_node_cidr_mask_size_ipv6 == null ? null : tostring(var.kubernetes_node_cidr_mask_size_ipv6)
    NETSY_ENDPOINT = var.netsy_endpoint
    NETSY_ACCESS_KEY_ID = var.netsy_access_key_id
    NETSY_SECRET_ACCESS_KEY = var.netsy_secret_access_key
    REGISTRY_ACCESS_KEY_ID = var.registry_access_key_id
    REGISTRY_SECRET_ACCESS_KEY = var.registry_secret_access_key
  } : key => value if value != null })
  certificates = {
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
    "ingress.server" = { kind = "server", cn = "ingress.server", dns = ["{{ .Instance.Hostname }}", "localhost", "example.com", "*.example.com"], ip = ["{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"], ttl = 8760 }
  }
  templates = {
    "control-plane" = {
      kind = "knc"
      arch = "amd64"
      vars = local.mutable_env
      userdata = { source = "inline", encoding = "base64", content = base64encode(data.podplane_userdata.knc_amd64.content) }
      files = { "mutable.env" = { kind = "env", template = merge(local.mutable_env, { NSTANCE_EBPF_PORTS = "{{ .Vars.NSTANCE_EBPF_PORTS }}" }) }, "kube2iam.client.crt" = { kind = "certificate", template = "kube2iam.client", key = { source = "agent", name = "kube2iam.client" } }, "kubelet.client.crt" = { kind = "certificate", template = "kubelet.client", key = { source = "agent", name = "kubelet.client" } }, "kubelet.server.crt" = { kind = "certificate", template = "kubelet.server", key = { source = "agent", name = "kubelet.server" } }, "front-proxy.client.crt" = { kind = "certificate", template = "front-proxy.client", key = { source = "agent", name = "front-proxy.client" } }, "kube-apiserver.client.crt" = { kind = "certificate", template = "kube-apiserver.client", key = { source = "agent", name = "kube-apiserver.client" } }, "kube-apiserver.server.crt" = { kind = "certificate", template = "kube-apiserver.server", key = { source = "agent", name = "kube-apiserver.server" } }, "kube-controller-manager.client.crt" = { kind = "certificate", template = "kube-controller-manager.client", key = { source = "agent", name = "kube-controller-manager.client" } }, "kube-scheduler.client.crt" = { kind = "certificate", template = "kube-scheduler.client", key = { source = "agent", name = "kube-scheduler.client" } }, "netsy.client.crt" = { kind = "certificate", template = "netsy.client", key = { source = "agent", name = "netsy.client" } }, "netsy.server.crt" = { kind = "certificate", template = "netsy.server", key = { source = "agent", name = "netsy.server" } }, "ingress.server.crt" = { kind = "certificate", template = "ingress.server", key = { source = "agent", name = "ingress.server" } }, "service-accounts.key" = { kind = "secret", source = "service-accounts.key" }, "service-accounts.pub" = { kind = "string", template = podplane_service_account_key.cluster.public_key_pem }, "tunnel.env" = {
        kind = "env"
        template = {
          TUNNEL_ENABLED = "true"
          TUNNEL_IMAGE = "index.docker.io/cloudflare/cloudflared@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
          TUNNEL_ARGS = "tunnel|--config|/run/tunnel/config.json|run"
          TUNNEL_FILES = "tunnel-config.json:/run/tunnel/config.json|tunnel-credentials.json:/run/tunnel/credentials.json|tunnel-ca.crt:/run/tunnel/ca.crt"
        }
      }, "tunnel-config.json" = {
        kind = "json"
        template = {
          "tunnel" = "cbe15ed8-50dc-4e69-a1d0-9e1909ec4ce8"
          "credentials-file" = "/run/tunnel/credentials.json"
          "metrics" = "127.0.0.1:2000"
          "ingress" = [{
            hostname = var.kubernetes_api_hostname
            service = "https://127.0.0.1:6443"
            originRequest = {
              caPool = "/run/tunnel/ca.crt"
              originServerName = var.kubernetes_api_hostname
            }
          }, {
            hostname = "example.com"
            service = "https://127.0.0.1:443"
            originRequest = {
              caPool = "/run/tunnel/ca.crt"
              originServerName = "example.com"
            }
          }, {
            hostname = "*.example.com"
            service = "https://127.0.0.1:443"
            originRequest = {
              caPool = "/run/tunnel/ca.crt"
              originServerName = "example.com"
            }
          }, {
            service = "http_status:404"
          }]
        }
      }, "tunnel-credentials.json" = {
        kind = "secret"
        source = "tunnel-credentials"
      }, "tunnel-ca.crt" = {
        kind = "string"
        template = "{{ .Cluster.CACert }}"
      } }
      args = {
        SourceImage = "projects/debian-cloud/global/images/family/debian-13"
      }
    }
    "ingress" = {
      kind = "knd"
      arch = "amd64"
      vars = local.mutable_env
      userdata = { source = "inline", encoding = "base64", content = base64encode(data.podplane_userdata.knd_amd64.content) }
      files = { "mutable.env" = { kind = "env", template = merge(local.mutable_env, { NSTANCE_EBPF_PORTS = "{{ .Vars.NSTANCE_EBPF_PORTS }}" }) }, "kube2iam.client.crt" = { kind = "certificate", template = "kube2iam.client", key = { source = "agent", name = "kube2iam.client" } }, "kubelet.client.crt" = { kind = "certificate", template = "kubelet.client", key = { source = "agent", name = "kubelet.client" } }, "kubelet.server.crt" = { kind = "certificate", template = "kubelet.server", key = { source = "agent", name = "kubelet.server" } }, "ingress.server.crt" = { kind = "certificate", template = "ingress.server", key = { source = "agent", name = "ingress.server" } } }
      args = {
        SourceImage = "projects/debian-cloud/global/images/family/debian-13"
      }
    }
    "nat" = {
      kind = "nat"
      arch = "amd64"
      userdata = { source = "inline", encoding = "base64", content = base64encode(data.podplane_userdata.nat_amd64.content) }
      files = { "mutable.env" = { kind = "env", template = {
        NAT_IPV4_SOURCE_CIDRS = "172.18.1.0/24,172.18.2.0/24"
      } } }
      args = {
        SourceImage = "projects/debian-cloud/global/images/family/debian-13"
      }
    }
  }
}

module "cluster" {
  source = "../../cache/deps/tf/modules/nstance/2.0.1/modules/cluster"
  depends_on = [terraform_data.deployment_modes]
  cluster_id = local.cluster_id
  name_prefix = local.name_prefix
  shards = ["us-central1-a"]
  secrets_prefix = "test-cluster-"
}

module "account__us_central1" {
  source = "../../cache/deps/tf/modules/nstance/2.0.1/modules/account"
  cluster = module.cluster
}

module "network__us_central1" {
  source = "../../cache/deps/tf/modules/nstance/2.0.1/modules/network"
  depends_on = [terraform_data.deployment_modes]
  cluster = module.cluster
  vpc_id = var.vpc_id
  vpc_cidr_ipv4 = var.vpc_cidr_ipv4
  ipv6_enabled = var.enable_ipv6
  nat_mode = var.nat_mode
  fixed_public_ipv4_count = var.fixed_public_ipv4_count
  project_id = local.provider_project
  region = local.provider_region
  subnets = local.subnets
}

module "shard_us_central1_a" {
  source = "../../cache/deps/tf/modules/nstance/2.0.1/modules/shard"
  cluster = module.cluster
  account = module.account__us_central1
  network = module.network__us_central1
  shard = "us-central1-a"
  zone = "us-central1-a"
  enable_iap = var.enable_iap
  enable_os_login = var.enable_os_login
  certificates = local.certificates
  templates = local.templates
  groups = {
    "default" = {
      "control-plane" = {
        template = "control-plane"
        size = var.pool_sizes["control-plane"]
        subnet_pool = "control-plane"
        machine_type = var.pool_instance_types["control-plane"]
        vars = merge(local.mutable_env, { NSTANCE_EBPF_PORTS = "443,6443" })
        load_balancers = ["cluster"]
      }
      "ingress" = {
        template = "ingress"
        size = var.pool_sizes["ingress"]
        subnet_pool = "ingress"
        machine_type = var.pool_instance_types["ingress"]
        vars = merge(local.mutable_env, { NSTANCE_EBPF_PORTS = "" })
      }
      "nat" = {
        template = "nat"
        subnet_pool = "public"
        machine_type = var.pool_instance_types["nat"]
        vars = {
          NAT_IPV4_SOURCE_CIDRS = "172.18.1.0/24,172.18.2.0/24"
        }
      }
    }
  }
  server_arch = "amd64"
  server_userdata = data.podplane_userdata.nst_amd64_us_central1_a.content
  nat = {
    "default" = {
      group = "nat"
      instance_type_ladder = ["e2-small", "e2-medium", "e2-standard-2"]
      public_addresses = module.network__us_central1.nat_public_addresses["public-us-central1-a"]
    }
  }
  tunnels = {
    "cluster" = {
      listeners = [{ target_port = 6443, proxy_port = 16443 }, { target_port = 443, proxy_port = 10443 }]
      image = "index.docker.io/cloudflare/cloudflared@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
      args = ["tunnel", "--config", "/run/tunnel/config.json", "run"]
      readiness_url = "http://127.0.0.1:2000/ready"
      files = {
        "/run/tunnel/config.json" = {
          kind = "json"
          template = {
            "tunnel" = "cbe15ed8-50dc-4e69-a1d0-9e1909ec4ce8"
            "credentials-file" = "/run/tunnel/credentials.json"
            "metrics" = "127.0.0.1:2000"
            "ingress" = [{
              hostname = var.kubernetes_api_hostname
              service = "https://127.0.0.1:16443"
              originRequest = {
                caPool = "/run/tunnel/ca.crt"
                originServerName = var.kubernetes_api_hostname
              }
            }, {
              hostname = "example.com"
              service = "https://127.0.0.1:10443"
              originRequest = {
                caPool = "/run/tunnel/ca.crt"
                originServerName = "example.com"
              }
            }, {
              hostname = "*.example.com"
              service = "https://127.0.0.1:10443"
              originRequest = {
                caPool = "/run/tunnel/ca.crt"
                originServerName = "example.com"
              }
            }, {
              service = "http_status:404"
            }]
          }
        }
        "/run/tunnel/credentials.json" = {
          kind = "secret"
          source = "tunnel-credentials"
        }
        "/run/tunnel/ca.crt" = {
          kind = "storage"
          source = "tunnel-ca.crt"
        }
      }
    }
  }
}

resource "podplane_service_account_key" "cluster" {
  secrets_provider = "google-secret-manager"
  secret_prefix = "test-cluster-"
  project = local.provider_project
  depends_on = [module.cluster]
}

resource "podplane_workload_ca_key" "cluster" {
  key_prefix = "test-cluster"
  secrets_provider = "google-secret-manager"
  project = "test-project"
  depends_on = [module.cluster]
}

resource "podplane_nstance_registration_key" "cluster" {
  secrets_provider = "google-secret-manager"
  secret_prefix = "test-cluster-"
  project = local.provider_project
  depends_on = [module.cluster]
}

resource "podplane_netsy_seed_gcs" "cluster" {
  cluster_config_path = "${path.module}/podplane.cluster.jsonc"
  bucket = google_storage_bucket.podplane_cluster["netsy"].name
  project = local.provider_project
  values_content = yamlencode({
    platform = {
      components = {
        values = {
          "nstance-operator" = {
            config = {
              shards = {
                "us-central1-a" = {
                  registration_addr = format("%s:8992", module.shard_us_central1_a.server_ips[0])
                  operator_addr = format("%s:8993", module.shard_us_central1_a.server_ips[0])
                }
              }
            }
          }
          "zot-registry" = {
            platform = { zotRegistry = {
              googleWorkloadIdentity = { provider = google_iam_workload_identity_pool_provider.cluster.name }
              storage = {
                driver = "gcs"
                bucket = google_storage_bucket.podplane_cluster["registry"].name
              }
            } }
            zot = {
              serviceAccount = { create = true, name = "zot-registry" }
              env = [{ name = "GOOGLE_APPLICATION_CREDENTIALS", value = "/var/run/google/identity/credentials.json" }]
              extraVolumes = [
                { name = "podplane-zot-config", configMap = { name = "zot-registry-config" } },
                { name = "google-identity", projected = { sources = [
                  { serviceAccountToken = { audience = "https://iam.googleapis.com/${google_iam_workload_identity_pool_provider.cluster.name}", expirationSeconds = 3600, path = "token" } },
                  { configMap = { name = "google-workload-identity" } }
                ] } }
              ]
              extraVolumeMounts = [
                { name = "podplane-zot-config", mountPath = "/etc/zot/config.json", subPath = "config.json", readOnly = true },
                { name = "google-identity", mountPath = "/var/run/google/identity", readOnly = true }
              ]
            }
          }
          "podplane-operator" = { podplane = { operator = { googleWorkloadIdentity = { provider = google_iam_workload_identity_pool_provider.cluster.name } } } }
          "secrets-store-csi-provider-gcp" = { platform = { secretsStoreCSIProviderGCP = { googleWorkloadIdentity = { provider = google_iam_workload_identity_pool_provider.cluster.name } } } }
          "cilium" = { platform = { cilium = { blockGoogleMetadata = true } } }
        }
        crds = { "secrets-store-csi-driver-crds" = { enabled = true } }
        apps = {
          "secrets-store-csi-driver" = { enabled = true }
          "secrets-store-csi-provider-gcp" = { enabled = true }
        }
      }
    }
  })
  nstance_bootstrap = {
    provider = "google-secret-manager"
    secret_prefix = "test-cluster-"
    signing_key_id = podplane_nstance_registration_key.cluster.id
    signing_key_version = podplane_nstance_registration_key.cluster.backend_version
    signing_key_fingerprint = podplane_nstance_registration_key.cluster.public_key_fingerprint
    cluster_id = local.cluster_id
    tenant = "default"
    namespace = "platform-nstance-operator"
    ca_bucket = module.cluster.bucket
  }
  depends_on = [google_storage_bucket_iam_member.podplane_registry_writer, google_iam_workload_identity_pool_provider.cluster, google_project_iam_member.podplane_secrets_catalog, google_project_iam_member.podplane_secrets_lifecycle, google_project_iam_member.podplane_secrets_reader, google_project_iam_member.podplane_ingress_state, podplane_workload_ca_key.cluster]
}

data "google_storage_bucket_object_content" "nstance_ca" {
  bucket = module.cluster.bucket
  name = "cluster/ca.crt"
  depends_on = [podplane_netsy_seed_gcs.cluster]
}

resource "google_storage_bucket_object" "tunnel_ca" {
  for_each = toset(["us-central1-a"])
  bucket = module.cluster.bucket
  name = "shard/${each.key}/tunnel-ca.crt"
  content = data.google_storage_bucket_object_content.nstance_ca.content
}
