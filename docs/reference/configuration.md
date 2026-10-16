---
title: "Configuration"
weight: 20
description: "Reference for Podplane configuration files"
aliases:
  - /docs/config-reference/
---

# Config Reference

Podplane uses JSONC (JSON with comments) configuration files to store cluster and OIDC server settings. These files are generated interactively by `podplane cluster create` and `podplane oidc create`.

## `podplane.cluster.jsonc`

This file is the user-facing projection of cluster configuration. It is created in the current directory by `podplane cluster create` and is required by many CLI commands such as `podplane login` and `podplane install`.

New cluster configs include a relative `$schema` reference to `./podplane.cluster.schema.json`. Podplane writes that schema file next to the config so editors such as VS Code can provide offline validation, completion, and hover documentation in shared infrastructure repositories. The source schema is checked into the Podplane repository at `schemas/podplane.cluster.schema.json`.

### Example

This example uses an existing user-managed tunnel and Nstance-managed NAT.

```jsonc
{
  "$schema": "./podplane.cluster.schema.json",
  "cluster": {
    "id": "internaltools-production",
    "name": "Internal Tools (Production)",
    "oidc": {
      "issuer_url": "https://auth.example.com"
    },
    "domains": [
      {
        "zone": "internaltools.example.com"
      }
    ],
    "tunnel": {
      "kind": "cloudflare",
      // Replace the example tunnel ID and secret name with your own.
      "id": "cbe15ed8-50dc-4e69-a1d0-9e1909ec4ce8",
      "credential_secret": "tunnel-credentials"
    },
    // ACME is disabled because the tunnel provider supplies public TLS.
    // Enable it only when using NLBs instead of a tunnel.
    // "acme": {
    //   "server": "https://acme-v02.api.letsencrypt.org/directory",
    //   "email": "platform-ops@example.com"
    // },
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
        // Public Nstance subnets keep outbound access independent of managed NAT.
        // Nstance management APIs remain private.
        "zones": {
          "us-east-1a": [
            { "v4cidr": "172.18.10.0/28", "v6cidr": "auto", "services": ["nat"], "public": true },
            { "v4cidr": "172.18.20.0/28", "v6cidr": "auto", "services": ["nstance"], "public": true },
            { "v4cidr": "172.18.1.0/26", "v6cidr": "auto", "pool": "control-plane" },
            { "v4cidr": "172.18.3.0/26", "v6cidr": "auto", "pool": "ingress" }
          ],
          "us-east-1b": [
            { "v4cidr": "172.18.11.0/28", "v6cidr": "auto", "services": ["nat"], "public": true },
            { "v4cidr": "172.18.21.0/28", "v6cidr": "auto", "services": ["nstance"], "public": true },
            { "v4cidr": "172.18.2.0/26", "v6cidr": "auto", "pool": "control-plane" },
            { "v4cidr": "172.18.4.0/26", "v6cidr": "auto", "pool": "ingress" }
          ]
        },
        // NLB alternative: omit cluster.tunnel, enable this block, add "nlb" to the
        // public NAT subnets' services, and enable api_load_balancer below.
        // Choose tunnels or NLBs before creating the infrastructure.
        // "load_balancers": {
        //   "main": {
        //     "public": true,
        //     "subnets": "public",
        //     "listeners": [
        //       { "port": 443, "pool": "ingress" }
        //     ]
        //   },
        //   "kubernetes-api": {
        //     "public": true,
        //     "subnets": "public",
        //     "listeners": [
        //       { "port": 6443, "pool": "control-plane" }
        //     ]
        //   }
        // },
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
    "nat": {
      "mode": "nstance",
      "groups": {
        "nat": { "arch": "arm64", "instance_type": "t4g.small", "subnet_pool": "public" }
      },
      "tenants": {
        "default": {
          "group": "nat",
          "instance_type_ladder": ["t4g.small", "t4g.medium", "t4g.large"]
        }
      }
    },
    "secrets": {
      "default_provider": "aws-secrets-manager",
      "providers": {
        "aws-secrets-manager": {
          "kind": "aws",
          "object_type": "secretsmanager",
          "region": "us-east-1"
        }
      }
    },
    "spiffe": {
      "trust_domain": "k8s.example.com"
    },
    "kubernetes": {
      "api_hostname": "k8s.example.com",
      // "api_load_balancer": "kubernetes-api",
      "cluster_cidr": ["100.64.0.0/10", "fd64::/48"],
      "service_cidr": ["198.18.0.0/15", "fdc6::/108"]
    },
    "registry": {
      "hostname": "registry.example.com",
      "ingress": { "enabled": false }
    },
    "seed": {
      "name": "recommended",
      "version": "v1.2.3-1",
      "digest": "sha512:00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
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

For the operational impact of changing cluster fields after initial deployment, including which changes are updated live (through an Nstance-pushed `mutable.env` file) and which changes cause Nstance to rotate Nstance-managed VMs, see [Change Impact: VMConfig Live Updates vs VM Rotation](vm-configuration.md#change-impact-runtime-update-vs-vm-rotation).

| Field | Description |
|---|---|
| `cluster.id` | Cluster identifier: lowercase alphanumeric with single internal hyphens, no leading, trailing, or consecutive hyphens, and at most 32 characters. `local`, `k8s`, and `oidc` are reserved. Auto-generated from `name` by the CLI, used as a prefix for cloud resources, and mapped to Nstance `cluster_id`. |
| `cluster.name` | Cluster name, used as a human-readable identifier |
| `cluster.oidc.issuer_url` | OIDC issuer URL for cluster authentication (e.g. `https://auth.example.com`) |
| `cluster.oidc.client_id` | OIDC client ID (defaults to `cluster.id` if not specified) |
| `cluster.oidc.username_claim` | Token claim used as the Kubernetes username (default: `sub`) |
| `cluster.oidc.groups_claim` | Token claim used for group membership (default: `groups`) |
| `cluster.oidc.signing_algs` | Allowed OIDC signing algorithms. Passed to kube-apiserver at runtime; vmconfig defaults to `["RS256"]` when omitted. |
| `cluster.acme.server` | Optional ACME directory override. Defaults to the Let's Encrypt production directory. |
| `cluster.acme.email` | Required ACME account email. Configuring `cluster.acme` enables operator-managed apex-and-wildcard ingress certificates for supported DNS providers; currently AWS Route53. Without a tunnel, omitting ACME retains operator-managed self-signed certificates delivered through Secrets Store CSI and SDS. Tunnel mode uses node-local Nstance-issued certificates instead and rejects ACME. Neither path stores certificate keys in Kubernetes Secrets. |
| `cluster.domains[]` | Array of domain configurations. The first domain is used as the default for ingress routing. |
| `cluster.domains[].zone` | Exact ingress apex (e.g. `staging.example.com`); Podplane creates apex and wildcard DNS records. |
| `cluster.domains[].load_balancer` | Named provider load balancer targeted by the apex and wildcard records. Defaults to `main`. |
| `cluster.domains[].provider` | Optional DNS provider. Omit it for manual DNS management. |
| `cluster.domains[].provider.kind` | DNS provider: `aws-route53`, `cloudflare`, `google-cloud-dns`, or `local`. Cluster Terraform generation manages Route53 on AWS and Cloud DNS on Google; tunnel DNS is user-managed. |
| `cluster.domains[].provider.region` | AWS Route53 region for DNS-01. If omitted, Podplane can infer it when exactly one matching AWS provider exists. |
| `cluster.domains[].provider.hosted_zone_id` | Optional explicit Route53 hosted zone ID. Use it to disambiguate or pin the exact hosted zone; otherwise, generated Terraform looks up the public hosted zone by domain name. |
| `cluster.domains[].provider.role_arn` | Advanced Route53 role override. Managed AWS clusters generate and inject a least-privilege ACME role automatically. |
| `cluster.domains[].provider.project` | Google Cloud project ID for Cloud DNS. |
| `cluster.domains[].provider.hosted_zone_name` | Google Cloud DNS managed-zone resource name, required for managed Cloud DNS records. |
| `cluster.tunnel` | Optional existing user-managed tunnel for both API and ingress, replacing NLBs. Podplane does not create or delete the tunnel. |
| `cluster.tunnel.kind` | Tunnel provider. The first supported provider is `cloudflare`; its runtime image is resolved from the pinned VM manifest. |
| `cluster.tunnel.id` | Existing tunnel identifier. For `cloudflare`, the UUID of a locally managed tunnel. |
| `cluster.tunnel.credential_secret` | Existing secret's logical name, relative to the cluster's Nstance secrets prefix. Store the complete tunnel credentials JSON as its value; normal and wake connectors share this reference. Not a local file path or inline secret. |
| `cluster.pools.<name>.arch` | CPU architecture for the pool's nodes. `amd64` or `arm64` |
| `cluster.pools.<name>.instance_type` | Cloud provider instance type (e.g. `t4g.medium` for AWS, `n2-standard-2` for Google Cloud) |
| `cluster.pools.<name>.size` | Minimum number of instances in the pool |
| `cluster.pools.<name>.disk_size` | Root disk size in GiB. Omit to use the provider's default. |
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
| `cluster.providers[].load_balancers.<name>.public` | Whether the named load balancer is internet-facing. |
| `cluster.providers[].load_balancers.<name>.subnets` | Subnet role in which the load balancer is placed, such as `public` or a pool name. |
| `cluster.providers[].load_balancers.<name>.listeners[]` | Explicit listeners exposed by the load balancer. Domains require `443 -> 443`; a managed Kubernetes API requires `api_port -> 6443` targeting the `control-plane` pool. |
| `cluster.providers[].load_balancers.<name>.listeners[].port` | External listener port. |
| `cluster.providers[].load_balancers.<name>.listeners[].target_port` | Port on target VMs. Defaults to the external port. |
| `cluster.providers[].load_balancers.<name>.listeners[].proxy_port` | AWS-only wake-proxy port. Defaults to `target_port`, then `port`; must be unique across listeners. Omit on Google Cloud. |
| `cluster.providers[].load_balancers.<name>.listeners[].pool` | Pool whose VMs are registered with this listener's target group. |
| `cluster.providers[].buckets` | Array of app-accessible object storage bucket names. Cloud bucket names are derived as `{cluster.id}-{name}`. |
| `cluster.providers[].roles.<name>.buckets` | Array of bucket names this role can access |
| `cluster.providers[].roles.<name>.permissions` | Resource access level - `read-write` or `read-only` (default: `read-write`) |
| `cluster.nat.mode` | Egress implementation: cloud-provider NAT (`provider`) or Nstance-managed NAT VMs (`nstance`). Independent of tunnels/NLBs. |
| `cluster.nat.groups.<name>` | NAT VM deployment settings: `arch`, starting `instance_type`, and public `subnet_pool`. Nstance manages the group size. |
| `cluster.nat.tenants.default` | NAT deployment group and instance type ladder for the default Kubernetes tenant. Timing and utilization thresholds use Nstance defaults. |
| `cluster.nat.tenants.default.group` | Name of the NAT deployment group. |
| `cluster.nat.tenants.default.instance_type_ladder` | Ordered, unique scaling choices; must include the group's starting instance type. |
| `cluster.nat.fixed_public_ipv4_count` | Number of static public IPv4 addresses to reserve for each NAT subnet when using Nstance-managed NAT. Defaults to `0`. |
| `cluster.kubernetes.api_hostname` | Required Kubernetes API hostname used by kubeconfig and the API server certificate. |
| `cluster.spiffe.trust_domain` | Required immutable SPIFFE trust domain for workload certificates. New clusters persist the initial `cluster.kubernetes.api_hostname`; existing clusters must explicitly add a valid value before upgrading infrastructure or enabling the operator. It allows only lowercase ASCII letters, digits, `.`, `-`, and `_`, up to 255 bytes. |
| `cluster.kubernetes.api_port` | External Kubernetes API port. Defaults to `6443` for NLB; tunnels require `443`. |
| `cluster.kubernetes.api_load_balancer` | Optional named provider load balancer for NLB API connectivity and DNS. Omit in tunnel mode. |
| `cluster.secrets.default_provider` | Default secrets provider name used by `podplane secret` and templates when `--provider` is omitted. |
| `cluster.secrets.providers` | Named secrets providers. Only provider-selection metadata belongs here; credentials are configured on the operator deployment. |
| `cluster.secrets.providers.<name>.kind` | Upstream Secrets Store CSI provider slug: `aws`, `gcp`, `vault`, or `openbao`. |
| `cluster.secrets.providers.<name>.key_prefix` | Optional backend key prefix for this provider. Defaults to `cluster.id`; set it only when clusters should intentionally share a provider backend prefix. For a default Vault/OpenBao provider, provision the workload CA key in KV-v2 field `value` at `<mount_path>/data/<key_prefix>/workload-ca-key` before creating the cluster. |
| `cluster.secrets.providers.<name>.object_type` | AWS Secrets Store CSI object type, such as `secretsmanager` or `ssmparameter`. |
| `cluster.secrets.providers.<name>.region` | AWS region used for provider API calls. |
| `cluster.secrets.providers.<name>.project_id` | Required Google Cloud project ID for a `gcp` provider. |
| `cluster.secrets.providers.<name>.location` | Optional regional Google Secret Manager location. |
| `cluster.secrets.providers.<name>.address` | Vault/OpenBao server address used by the operator and rendered into generated `SecretProviderClass` objects. Vault and OpenBao addresses must use HTTPS. |
| `cluster.secrets.providers.<name>.mount_path` | Vault/OpenBao KV-v2 mount name. Defaults to `secret`. |
| `cluster.secrets.providers.<name>.ca_cert` | Optional PEM CA bundle for a Vault/OpenBao endpoint served by a private CA. Local fakevault config sets this automatically. |
| `cluster.secrets.providers.<name>.auth_path` | Vault/OpenBao Kubernetes/JWT auth mount path used by the operator. Defaults to `auth/kubernetes`. |
| `cluster.secrets.providers.<name>.operator_role` | Vault/OpenBao role used by the operator service account. Defaults to `podplane-operator`. Workload CSI reads use the binding/service account role separately. |
| `cluster.kubernetes.cluster_cidr` | CIDR ranges for Pod IPs, joined with commas for kube-controller-manager `--cluster-cidr` |
| `cluster.kubernetes.service_cidr` | CIDR ranges for Service ClusterIPs, joined with commas for kube-apiserver `--service-cluster-ip-range` |
| `cluster.registry.hostname` | Cluster registry hostname used by the node-local registry, `podplane push`, and optional Docker-push-compatible ingress. |
| `cluster.registry.ingress.enabled` | Enables optional Docker-push-compatible registry ingress/token-service routing. Disabled by default; `podplane push` does not require ingress. |
| `cluster.seed.name` | Podplane seed file to use when creating the Netsy bootstrap file - `recommended`, `minimal`, or `none`. Leave `cluster.seed` as an empty object to seed no platform-components state, leaving a bare cluster that must be bootstrapped manually. |
| `cluster.seed.version` | Podplane seeds release version used for the selected seed file, e.g. `v1.2.3-1`. Generated configs pin this to the known available seed version. Omit inside an empty `cluster.seed` object. |
| `cluster.seed.digest` | Expected SHA-512 digest of the selected seed snapshot. Generated configs pin this from the seeds manifest; seed resolution verifies the downloaded file before interpolation. |
| `cluster.components.registry.mirror.enabled` | Render platform component image references through the configured registry mirror. Local clusters enable this automatically so seeded components pull from the VM-hosted registry backed by the local dependency cache. |
| `cluster.components.registry.mirror.hostname` | Advanced shared-mirror hostname override. Defaults to `cluster.registry.hostname`. |
| `cluster.components.registry.mirror.prefix` | Advanced mirror path-prefix override. Defaults to `mirror`; `/mirror/` cleans to `mirror`, and `/` or an empty string means no prefix. |
| `cluster.components.source.url` | Git repository URL used by `platform-components` as the source for component Helm charts. Defaults to the published Podplane components repository when omitted. |
| `cluster.components.source.ref.branch` | Git branch to use for component Helm charts. |
| `cluster.components.source.ref.tag` | Git tag to use for component Helm charts. Mutually exclusive with other `source.ref` selectors. |
| `cluster.components.source.ref.semver` | Git semver range to use for component Helm charts. Mutually exclusive with other `source.ref` selectors. |
| `cluster.components.source.ref.commit` | Git commit to use for component Helm charts. Mutually exclusive with other `source.ref` selectors. |
| `cluster.components.source.secretRef.name` | Optional Flux Git credentials Secret name in the `platform-components` namespace. Use this for private/enterprise components repos; Podplane wires the reference but does not create the Secret. |

### Kubernetes API & Ingress, NAT, and sleep

`cluster.tunnel` exposes both the Kubernetes API and ingress through the selected tunnel
provider. Omit it to use the infrastructure provider's NLBs instead. Tunnel mode
does not accept load-balancer settings. Switching between tunnels and NLBs requires
deleting and redeploying the infrastructure.

Create a locally managed tunnel, point the API and ingress apex/wildcard DNS records
at it, and configure hostname-scoped HTTPS redirects with the tunnel provider before
deployment. Store the complete credentials-file JSON in the cluster's secrets provider
backend under `credential_secret` (relative to its configured prefix). Podplane does
not create, modify or delete these provider resources and needs no tunnel-provider
API token.

Tunnel API endpoints use HTTPS port `443` and public CA roots; omit `--ca-cert`
when logging in. NLB API endpoints default to `6443` with the Kubernetes CA.
The API origin remains direct kube-apiserver `6443`, independent of Envoy.

Do not configure `cluster.acme` with a tunnel: the provider supplies public-edge
TLS. Each Envoy node gets one node-issued ingress certificate covering all
configured apex/wildcard domains, localhost, loopbacks and its assigned IPs.
Tunnel processes verify both the Kubernetes API and Envoy ingress against the Nstance
cluster CA and the configured origin hostname.

NAT is independent of tunnels/NLBs. Nstance-managed NAT requires public NAT
and Nstance subnets in each zone. The Nstance Server VM needs outbound cloud API, storage
and tunnel-provider access even when its managed NAT VMs are removed during sleep;
public placement avoids that dependency without exposing its management APIs.
Changing NAT mode preserves node subnets; changing their CIDRs is a separate migration.

AWS/Google `recommended` clusters support [`cluster sleep`](cli/commands/cluster-sleep.md)
and wake on API or ingress traffic.

### Certificates and authentication

Cluster infrastructure creates or adopts a dedicated `workload-ca-key` in AWS
Secrets Manager, AWS SSM Parameter Store, or Google Secret Manager, even for
`minimal` and `none` seeds. Generated OpenTofu state stores provider metadata
and a public-key fingerprint only, never the private key. Customers using an
external Vault or OpenBao installation provision the key before creating the
cluster. Local clusters create it automatically in their encrypted local
OpenBao-compatible store.

Kubernetes usernames default to the token's `sub` claim without an issuer
prefix. Truster places normalized email addresses in `sub` for user logins and
namespaced `trusted:` identities in `sub` for service logins. Set
`cluster.oidc.username_claim` when another OIDC provider requires a different
claim.

**Validation rules:**
- `cluster.id` must be lowercase alphanumeric with hyphens only, no leading/trailing/consecutive hyphens, max 32 characters.
- `vpc.id` and `vpc.v4cidr`/`vpc.v6cidr` are mutually exclusive - specify an existing VPC ID or CIDRs to create a new VPC, not both.
- Subnet `id` and `v4cidr`/`v6cidr` are mutually exclusive - specify an existing subnet ID or CIDRs to create a new subnet, not both.
- Each subnet must have exactly one of `pool` or `services` - not both.
- Subnets with `nat` or `nlb` in `services` must be `public: true`.
- Role `buckets` entries must reference bucket names declared in the same provider's `buckets` array.

## `podplane.oidc.jsonc`

This file stores configuration for a [Truster](https://truster.dev) server deployment. It is created in the current directory by `podplane oidc create` and is required by `podplane oidc delete`.

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
      "client_secret_arn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:truster-connector-secret"
    },
    "signing_key_secret_arn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:truster-signing-key",
    "encryption_key_secret_arn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:truster-encryption-key",
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
| `oidc.provider.kind` | Infrastructure provider. Only `aws` is currently supported. |
| `oidc.provider.region` | Provider region to deploy into (e.g. `us-east-1`) |
| `oidc.provider.account` | Provider account identifier (e.g. AWS account ID) |
| `oidc.provider.profile` | Provider credentials profile (e.g. AWS CLI profile name) |
| `oidc.hostname` | The hostname for the OIDC server (e.g. `auth.example.com`) |
| `oidc.domain.zone` | Domain zone for the hostname (e.g. `example.com`) |
| `oidc.domain.provider.kind` | Managed DNS provider. Only `aws` is currently supported. |
| `oidc.connector.kind` | Upstream OAuth provider - `google` or `github` |
| `oidc.connector.client_secret_arn` | ARN of the AWS Secrets Manager secret containing the OAuth client ID and secret |
| `oidc.signing_key_secret_arn` | ARN of the AWS Secrets Manager secret containing the OIDC signing key |
| `oidc.encryption_key_secret_arn` | ARN of the AWS Secrets Manager secret containing Truster's encryption key. Required for GitHub connectors and recommended when enabling refresh tokens. |
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

See [CLI Overview](cli/overview.md#config-files--context) for recommended directory structure.
