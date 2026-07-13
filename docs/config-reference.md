---
title: "Config Reference"
weight: 100
description: "Reference for Podplane configuration files"
---

# Config Reference

Podplane uses JSONC (JSON with comments) configuration files to store cluster and OIDC server settings. These files are generated interactively by `podplane cluster create` and `podplane oidc create`.

## `podplane.cluster.jsonc`

This file is the user-facing projection of cluster configuration. It is created in the current directory by `podplane cluster create` and is required by many CLI commands such as `podplane login` and `podplane install`.

New cluster configs include a relative `$schema` reference to `./podplane.cluster.schema.json`. Podplane writes that schema file next to the config so editors such as VS Code can provide offline validation, completion, and hover documentation in shared infrastructure repositories. The source schema is checked into the Podplane repository at `schemas/podplane.cluster.schema.json`.

### Example

```jsonc
{
  "$schema": "./podplane.cluster.schema.json",
  "cluster": {
    "id": "internaltools-production",
    "name": "Internal Tools (Production)",
    "oidc": {
      "issuer_url": "https://auth.example.com"
    },
    "acme": {
      "server": "https://acme-v02.api.letsencrypt.org/directory",
      "email": "platform-ops@example.com"
    },
    "domains": [
      {
        "zone": "internaltools.example.com",
        "provider": {
          "kind": "aws",
          "account": "123456789012",
          "profile": "default",
          "region": "us-east-1",
          "hosted_zone_id": "Z0123456789",
          "role_arn": "arn:aws:iam::123456789012:role/podplane-cert-manager-dns01"
        }
      }
    ],
    "pools": {
      "control-plane": {
        "arch": "arm64",
        "instance_type": "t4g.medium",
        "size": 1
      },
      "ingress": {
        "arch": "arm64",
        "instance_type": "t4g.medium",
        "size": 1
      }
    },
    "providers": [
      {
        "kind": "aws",
        "region": "us-east-1",
        "account": "123456789012",
        "profile": "default",
        "tags": {
          "podplane:environment": "production",
          "podplane:managed-by": "podplane"
        },
        "vpc": {
          "v4cidr": "172.18.0.0/16",
          "v6cidr": "auto"
        },
        "zones": {
          "us-east-1a": [
            { "v4cidr": "172.18.10.0/28", "v6cidr": "auto", "services": ["nat", "nlb"], "public": true },
            { "v4cidr": "172.18.20.0/28", "v6cidr": "auto", "services": ["nstance"] },
            { "v4cidr": "172.18.1.0/24", "v6cidr": "auto", "pool": "control-plane" },
            { "v4cidr": "172.18.3.0/24", "v6cidr": "auto", "pool": "ingress" }
          ],
          "us-east-1b": [
            { "v4cidr": "172.18.11.0/28", "v6cidr": "auto", "services": ["nat", "nlb"], "public": true },
            { "v4cidr": "172.18.21.0/28", "v6cidr": "auto", "services": ["nstance"] },
            { "v4cidr": "172.18.2.0/24", "v6cidr": "auto", "pool": "control-plane" },
            { "v4cidr": "172.18.4.0/24", "v6cidr": "auto", "pool": "ingress" }
          ]
        },
        "load_balancer": {
          "public": true,
          "listeners": [
            { "port": 6443, "pool": "control-plane" },
            { "port": 80, "pool": "ingress" },
            { "port": 443, "pool": "ingress" }
          ]
        },
        "buckets": ["uploads", "assets"],
        "roles": {
          "app-storage": {
            "buckets": ["uploads", "assets"]
          },
          "analytics": {
            "buckets": ["uploads"],
            "permissions": "read-only"
          }
        }
      }
    ],
    "kubernetes": {
      "cluster_cidr": ["100.64.0.0/10", "fd64::/48"],
      "service_cidr": ["198.18.0.0/15", "fdc6::/108"]
    },
    "registry": {
      "hostname": "registry.example.com",
      "ingress": { "enabled": false }
    },
    "seed": {
      "name": "recommended",
      "version": "v1.2.3-1"
    },
    "components": {
      "registry": {
        "mirror": {
          "enabled": true,
          "prefix": "mirror"
        }
      },
      "source": {
        "url": "https://github.com/podplane/components.git",
        "ref": {
          "semver": "^1.2.3"
        }
      }
    }
  }
}
```

### Fields

For the operational impact of changing cluster fields after initial deployment, including which changes are updated live (through an Nstance-pushed `mutable.env` file) and which changes cause Nstance to rotate Nstance-managed VMs, see [Change Impact: VMConfig Live Updates vs VM Rotation](vmconfig.md#change-impact-runtime-update-vs-vm-rotation).

| Field | Description |
|---|---|
| `cluster.id` | Cluster identifier - lowercase alphanumeric and hypens, max 32 characters. Auto-generated from `name` by the CLI, used as a prefix for cloud resources and maps to Nstance `cluster_id`. |
| `cluster.name` | Cluster name, used as a human-readable identifier |
| `cluster.oidc.issuer_url` | OIDC issuer URL for cluster authentication (e.g. `https://auth.example.com`) |
| `cluster.oidc.client_id` | OIDC client ID (defaults to `cluster.id` if not specified) |
| `cluster.oidc.username_claim` | Token claim used as the username (default: `email`) |
| `cluster.oidc.groups_claim` | Token claim used for group membership (default: `groups`) |
| `cluster.oidc.signing_algs` | Allowed OIDC signing algorithms. Passed to kube-apiserver at runtime; vmconfig defaults to `["RS256"]` when omitted. |
| `cluster.acme.server` | ACME directory URL used for production ingress certificates. When set with `cluster.acme.email`, Podplane configures cert-manager DNS-01 issuers. Omit `cluster.acme` for local/self-signed ingress certificates. |
| `cluster.acme.email` | ACME account email address for expiry and account notices. |
| `cluster.domains[]` | Array of domain configurations. The first domain is used as the default for ingress routing. |
| `cluster.domains[].zone` | Domain zone (e.g. `internaltools.example.com`) |
| `cluster.domains[].provider.kind` | Domain DNS provider - `aws`, `cloudflare`, `google`, or `local` for local clusters. |
| `cluster.domains[].provider.region` | AWS Route53 region for DNS-01. If omitted, Podplane can infer it when exactly one matching AWS provider exists. |
| `cluster.domains[].provider.hosted_zone_id` | AWS Route53 hosted zone ID for the domain (optional but recommended when using AWS DNS-01). |
| `cluster.domains[].provider.role_arn` | AWS IAM role ARN cert-manager should assume for Route53 DNS-01 changes. |
| `cluster.domains[].provider.secret_provider_class_name` | Existing Secrets Store CSI `SecretProviderClass` to mount so external secret material can be synced before cert-manager uses it. |
| `cluster.domains[].provider.secret_name` | Kubernetes Secret name containing DNS provider credentials. Required for Cloudflare DNS-01 and optional for Google CloudDNS when using a service account key. |
| `cluster.domains[].provider.secret_key` | Key inside `secret_name`. Defaults to `api-token` for Cloudflare and `service-account.json` for Google CloudDNS. |
| `cluster.domains[].provider.project` | Google Cloud project ID for CloudDNS DNS-01. |
| `cluster.domains[].provider.hosted_zone_name` | Google CloudDNS managed zone name for the domain (optional). |
| `cluster.pools.<name>.arch` | CPU architecture for the pool's nodes. `amd64` or `arm64` |
| `cluster.pools.<name>.instance_type` | Cloud provider instance type (e.g. `t4g.medium` for AWS, `n2-standard-2` for Google Cloud) |
| `cluster.pools.<name>.size` | Minimum number of instances in the pool |
| `cluster.pools.<name>.disk_size` | Root volume size in GB (default: `100`) |
| `cluster.providers[]` | Array of cloud provider configurations (supports multi-cloud). The available fields vary by `kind` - the fields below apply to `aws` and `google`. |
| `cluster.providers[].kind` | Cloud provider - `aws`, `google`, or `proxmox` |
| `cluster.providers[].region` | Provider region (e.g. `us-east-1` for AWS, `us-central1` for Google Cloud) |
| `cluster.providers[].account` | AWS account ID (AWS only) |
| `cluster.providers[].profile` | AWS CLI profile name (AWS only) |
| `cluster.providers[].project` | Google Cloud project ID (Google Cloud only) |
| `cluster.providers[].tags` | Key-value map of tags to apply to all cloud resources created by this provider (e.g. `{"podplane:environment": "production"}`) |
| `cluster.providers[].vpc.id` | ID of an existing VPC to use (alternative to creating a new one with `v4cidr`) |
| `cluster.providers[].vpc.v4cidr` | IPv4 CIDR block for creating a new VPC (or specify an `id` to use existing) |
| `cluster.providers[].vpc.v6cidr` | IPv6 CIDR for the VPC - `"auto"` for provider-assigned or an explicit CIDR (optional, for dual-stack) |
| `cluster.providers[].zones.<zone>[]` | Array of subnet definitions for this zone. Each entry has either `pool` (for node subnets) or `services` (for infrastructure subnets). |
| `cluster.providers[].zones.<zone>[].pool` | Name of the pool this subnet belongs to (mutually exclusive with `services`) |
| `cluster.providers[].zones.<zone>[].services` | Infrastructure services this subnet hosts - `nstance`, `nat`, and/or `nlb` (array, mutually exclusive with `pool`) |
| `cluster.providers[].zones.<zone>[].public` | Whether the subnet is public - has a route to an internet gateway (default: `false`) |
| `cluster.providers[].zones.<zone>[].id` | ID of an existing subnet to use (alternative to creating a new one with `v4cidr`) |
| `cluster.providers[].zones.<zone>[].v4cidr` | IPv4 CIDR block for creating a new subnet (or specify an `id` to use existing) |
| `cluster.providers[].zones.<zone>[].v6cidr` | IPv6 CIDR for the subnet - `"auto"` for provider-assigned or an explicit CIDR (optional, for dual-stack) |
| `cluster.providers[].load_balancer.public` | Whether the load balancer is internet-facing (default: `false`) |
| `cluster.providers[].load_balancer.listeners[]` | Array of listener configurations |
| `cluster.providers[].load_balancer.listeners[].port` | Port to listen on |
| `cluster.providers[].load_balancer.listeners[].pool` | Pool to route traffic to |
| `cluster.providers[].load_balancer.listeners[].target_port` | Port on the target nodes (optional, defaults to `port`) |
| `cluster.providers[].buckets` | Array of app-accessible object storage bucket names. Cloud bucket names are derived as `{cluster.id}-{name}`. |
| `cluster.providers[].roles.<name>.buckets` | Array of bucket names this role can access |
| `cluster.providers[].roles.<name>.permissions` | Resource access level - `read-write` or `read-only` (default: `read-write`) |
| `cluster.secrets.default_provider` | Default secrets provider name used by `podplane secret` and templates when `--provider` is omitted. |
| `cluster.secrets.providers` | Named secrets providers. Only provider-selection metadata belongs here; credentials are configured on the operator deployment. |
| `cluster.secrets.providers.<name>.kind` | Secrets provider kind, such as `aws`, `gcp`, or `openbao`. |
| `cluster.secrets.providers.<name>.key_prefix` | Optional backend key prefix for this provider. Defaults to `cluster.id`; set it only when clusters should intentionally share a provider backend prefix. |
| `cluster.secrets.providers.<name>.object_type` | AWS Secrets Store CSI object type, such as `secretsmanager` or `ssmparameter`. |
| `cluster.secrets.providers.<name>.address` | Vault/OpenBao server address used by the operator and rendered into generated `SecretProviderClass` objects. |
| `cluster.secrets.providers.<name>.mount_path` | Vault/OpenBao KV-v2 mount name. Defaults to `secret`. |
| `cluster.secrets.providers.<name>.ca_cert` | Optional PEM CA bundle for a Vault/OpenBao endpoint served by a private CA. Local fakevault config sets this automatically. |
| `cluster.secrets.providers.<name>.auth_path` | Vault/OpenBao Kubernetes/JWT auth mount path used by the operator. Defaults to `auth/kubernetes`. |
| `cluster.secrets.providers.<name>.operator_role` | Vault/OpenBao role used by the operator service account. Defaults to `podplane-operator`. Workload CSI reads use the binding/service account role separately. |
| `cluster.kubernetes.api_hostname` | External hostname for the kube-apiserver (defaults to `k8s.<first domain zone>`) |
| `cluster.kubernetes.api_port` | Port for the kube-apiserver (default: `6443`) |
| `cluster.kubernetes.cluster_cidr` | CIDR ranges for Pod IPs, joined with commas for kube-controller-manager `--cluster-cidr` |
| `cluster.kubernetes.service_cidr` | CIDR ranges for Service ClusterIPs, joined with commas for kube-apiserver `--service-cluster-ip-range` |
| `cluster.registry.hostname` | Cluster registry hostname used by node-local Zot, `podplane push`, and optional Docker-push-compatible ingress. |
| `cluster.registry.ingress.enabled` | Enables optional Docker-push-compatible registry ingress/token-service routing. Disabled by default; `podplane push` does not require ingress. |
| `cluster.seed.name` | Podplane seed file to use when creating the Netsy bootstrap file - `recommended`, `minimal`, or `none`. Leave `cluster.seed` as an empty object to seed no platform-components state, leaving a bare cluster that must be bootstrapped manually. |
| `cluster.seed.version` | Podplane seeds release version used for the selected seed file, e.g. `v1.2.3-1`. Generated configs pin this to the known available seed version. Omit inside an empty `cluster.seed` object. |
| `cluster.components.registry.mirror.enabled` | Render platform component image references through the configured registry mirror. Local clusters enable this automatically so seeded components pull from the VM-hosted registry backed by the local dependency cache. |
| `cluster.components.registry.mirror.hostname` | Advanced shared-mirror hostname override. Defaults to `cluster.registry.hostname`. |
| `cluster.components.registry.mirror.prefix` | Advanced mirror path-prefix override. Defaults to `mirror`; `/mirror/` cleans to `mirror`, and `/` or an empty string means no prefix. |
| `cluster.components.source.url` | Git repository URL used by `platform-components` as the source for component Helm charts. Defaults to the published Podplane components repository when omitted. |
| `cluster.components.source.ref.branch` | Git branch to use for component Helm charts. |
| `cluster.components.source.ref.tag` | Git tag to use for component Helm charts. Mutually exclusive with other `source.ref` selectors. |
| `cluster.components.source.ref.semver` | Git semver range to use for component Helm charts. Mutually exclusive with other `source.ref` selectors. |
| `cluster.components.source.ref.commit` | Git commit to use for component Helm charts. Mutually exclusive with other `source.ref` selectors. |
| `cluster.components.source.secretRef.name` | Optional Flux Git credentials Secret name in the `platform-components` namespace. Use this for private/enterprise components repos; Podplane wires the reference but does not create the Secret. |

**Validation rules:**
- `cluster.id` must be lowercase alphanumeric with hyphens only, no leading/trailing/consecutive hyphens, max 32 characters.
- `vpc.id` and `vpc.v4cidr`/`vpc.v6cidr` are mutually exclusive - specify an existing VPC ID or CIDRs to create a new VPC, not both.
- Subnet `id` and `v4cidr`/`v6cidr` are mutually exclusive - specify an existing subnet ID or CIDRs to create a new subnet, not both.
- Each subnet must have exactly one of `pool` or `services` - not both.
- Subnets with `nat` or `nlb` in `services` must be `public: true`.
- Role `buckets` entries must reference bucket names declared in the same provider's `buckets` array.

## `podplane.oidc.jsonc`

This file stores configuration for an [Easy OIDC](https://easy-oidc.dev) server deployment. It is created in the current directory by `podplane oidc create` and is required by `podplane oidc delete`.

New OIDC configs include a relative `$schema` reference to `./podplane.oidc.schema.json`. Podplane writes that schema file next to the config so editors such as VS Code can provide offline validation, completion, and hover documentation in shared infrastructure repositories. The source schema is checked into the Podplane repository at `schemas/podplane.oidc.schema.json`.

### Example

```jsonc
{
  "$schema": "./podplane.oidc.schema.json",
  "oidc": {
    "provider": {
      "kind": "aws",
      "region": "us-east-1",
      "account": "123456789012",
      "profile": "default"
    },
    "hostname": "auth.example.com",
    "domain": {
      "zone": "example.com",
      "provider": {
        "kind": "aws"
      }
    },
    "connector": {
      "kind": "google",
      "client_secret_arn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:easy-oidc-connector-secret"
    },
    "signing_key_secret_arn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:easy-oidc-signing-key",
    "default_redirect_uris": ["http://localhost:8000"],
    "clients": {
      "kubelogin-prod": {
        "groups_override": "prod-groups"
      },
      "kubelogin-dev": {}
    },
    "groups_overrides": {
      "prod-groups": {
        "demo@example.com": ["prod-admins", "devs"]
      }
    }
  }
}
```

### Fields

| Field | Description |
|---|---|
| `oidc.provider.kind` | Cloud provider - `aws` (Google Cloud and Azure are planned) |
| `oidc.provider.region` | Provider region to deploy into (e.g. `us-east-1`) |
| `oidc.provider.account` | Provider account identifier (e.g. AWS account ID) |
| `oidc.provider.profile` | Provider credentials profile (e.g. AWS CLI profile name) |
| `oidc.hostname` | The hostname for the OIDC server (e.g. `auth.example.com`) |
| `oidc.domain.zone` | Domain zone for the hostname (e.g. `example.com`) |
| `oidc.domain.provider.kind` | Domain DNS provider - `aws`, `cloudflare`, or `google` |
| `oidc.connector.kind` | Upstream OAuth provider - `google` or `github` |
| `oidc.connector.client_secret_arn` | ARN of the AWS Secrets Manager secret containing the OAuth client ID and secret |
| `oidc.signing_key_secret_arn` | ARN of the AWS Secrets Manager secret containing the OIDC signing key |
| `oidc.default_redirect_uris` | Default redirect URIs applied to clients that don't specify their own |
| `oidc.clients.<name>` | Map of OIDC client configurations |
| `oidc.clients.<name>.groups_override` | Name of a groups override to apply to the specified client (optional) |
| `oidc.groups_overrides.<name>` | Static group mappings, where each key is an email and the value is an array of group names |

## File Location

By default, the CLI looks for config files in the current working directory. You can specify an alternate path using the `-f` flag:

```bash
podplane login -f ./my-cluster/podplane.cluster.jsonc
podplane oidc delete -f ./auth-server/podplane.oidc.jsonc
```

See [CLI Overview](cli-overview.md#config-files--context) for recommended directory structure.
