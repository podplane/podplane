---
title: "VM Configuration"
weight: 60
description: "How Podplane configures VMs using vmconfig"
aliases:
  - /docs/vmconfig/
---

# VM Configuration

The "VM Layer" of the Podplane [Architecture](architecture.md) is responsible for getting Pods scheduled on a VM. This is essentially Netsy + core Kubernetes + containerd, configured by [vmconfig](https://github.com/podplane/vmconfig) - a minimal configuration system designed for Debian-based Linux VMs, written in Bash.

## How It Works

The user-data script which invokes the `vmconfig` entrypoint is responsible for supplying the package dependencies, which is defined by the "kind" of VMs you want `vmconfig` to configure:

1. `knd` creates a Kubernetes Data Plane / Worker node, which runs kubelet, containerd, and supporting services.

2. `knc` creates a Kubernetes Control Plane node, which is essentially a base of `knd` + adds Netsy (as an etcd alternative), kube-apiserver, kube-scheduler, and kube-controller-manager.

3. `nst` creates a Nstance Server VM, including the optional sleep proxy and wake-tunnel supervisor.

4. `nat` creates a dedicated NAT VM, without kubelet, containerd, or a registry.

The `knd`, `knc`, and `nst` kinds include an optional node-local Podplane Registry. It runs when registry storage and a registry hostname are configured.

## Deployed Clusters

The flow for clusters you create via Podplane CLI is:

1. CLI generates and deploys infrastructure-as-code
2. Nstance auto-scales VMs using the Podplane-generated cloud-init user-data script
3. Each VM's user-data script downloads the relevant dependencies, extracts the `vmconfig` archive, and runs its install/configure scripts
4. vmconfig sets up all required services for the VM's kind (`knd` = data plane, `knc` = control plane)
5. Control Plane nodes run Netsy backed by object storage, replacing the need for etcd and on-disk state

## Local Clusters

For local VMs run via the Podplane CLI, the same `vmconfig` configuration is used to run a single `knc` VM per local cluster.

The CLI itself is responsible for downloading/caching dependencies and serving them to the VM via a webserver it runs in the background via the [local start](cli/commands/local-start.md) command. The same webserver also hosts a fake OIDC server and fake S3 server for local clusters. You can also use [local server](cli/commands/local-server.md) to run the webserver directly.

## Container Registry

VMs run [Podplane Registry](https://github.com/podplane/registry), a read-only OCI registry backed by the configured registry object-storage bucket.

Podplane supports component and template image "mirroring" whereby the registry bucket stores a copy of all required container images if mirroring is enabled.

To use this mirror, components render explicit image references such as `<registry-hostname>/mirror/<original-registry>/<repository>:<tag>`. This differs to user-pushed app images which live under `<registry-hostname>/apps/...`. Podplane intentionally does not make the node-local registry a transparent containerd pull-through cache for all image pulls - while that would have made configuring image references easier without the `<registry-hostname>/` prefix, the decision to use explicit references was because:

1. it is obvious from the rendered Kubernetes manifest when an image is using the Podplane built-in registry, even though components and templates need to do more work to render the correct image references (e.g. you have to override an off-the-shelf Helm chart values file to change the images used)
2. user workloads keep native Kubernetes registry authentication behavior, including per-namespace and per-service-account `imagePullSecrets`; a transparent pull-through cache would need to authenticate to upstream registries itself and would not naturally receive the pod's upstream registry credentials

Templates and components should therefore use canonical upstream image references by default, and only render mirrored references intentionally when Podplane owns the image selection and registry mirror behavior.

## Package Dependencies

__Data Plane & Control Plane VMs__:
- [nstance-agent](https://nstance.dev/docs/components/nstance-agent/) to register VMs
- [fluent-bit](https://docs.fluentbit.io/manual) for log forwarding
  - [libpq5](https://packages.debian.org/trixie/libpq5) runtime dependency for fluent-bit
- [kube2iam](https://github.com/jtblin/kube2iam) for providing IAM Roles to pods
- [containerd](https://containerd.io/docs/main/) the container runtime 
- [runc](https://github.com/opencontainers/runc) for containerd to spawn OCI containers
- [gVisor](https://gvisor.dev/) the sandboxed runtime Pods use by default (`knd` and `knc` only; `nst` runs only runc)
- [kubelet](https://kubernetes.io/docs/reference/command-line-tools-reference/kubelet/) for running node pods
  - [uidmap](https://packages.debian.org/sid/uidmap) runtime dependency of kubelet for getsubids
  - [libsubid5](https://packages.debian.org/trixie/libsubid5) runtime dependency of kubelet for getsubids
- [cni-plugins](https://github.com/containernetworking/plugins) the reference CNI plugins
- [Podplane Registry](https://github.com/podplane/registry) the read-only node-local container registry

__Control Plane VMs__:
- [netsy](https://netsy.dev/docs/design/) as an etcd alternative
- [kube-apiserver](https://kubernetes.io/docs/reference/command-line-tools-reference/kube-apiserver/) the Kubernetes API server
- [kube-scheduler](https://kubernetes.io/docs/reference/command-line-tools-reference/kube-scheduler/) the Kubernetes scheduler
- [kube-controller-manager](https://kubernetes.io/docs/reference/command-line-tools-reference/kube-controller-manager/) the core Kubernetes control loops

## Env Vars & Service Configuration

While most of the configuration for each VM kind is the same, there are a set of variable inputs to the `vmconfig` package, propagated to each service via environment variable `.env` files:

1. `/opt/podplane/etc/user-data.env`: immutable, created on first-boot by the cloud-init `user-data.sh` script, read only by the `/opt/podplane/bin/configure.sh` script. Contains the instance identity and minimum configuration needed to register nstance-agent. The Nstance registration nonce and CA certificate are written to separate identity files. It can also contain `IMMUTABLE_SSH_AUTHORIZED_KEYS` for debugging failures before nstance-agent becomes healthy; note that changing or removing those keys requires rotating affected VMs, so the `SSH_AUTHORIZED_KEYS` mutable alternative is recommended for general SSH access.

2. `/opt/podplane/etc/detected.env`: immutable, created during the first run of the `/opt/podplane/bin/bootstrap.sh` script, and read only by the `/opt/podplane/bin/configure.sh` script. Contains for example the detected instance hostname and IPv4 / IPv6 addresses.

3. `/opt/podplane/etc/mutable.env`: mutable, created during the first run of the `/opt/podplane/bin/bootstrap.sh` script, able to be updated by the `/opt/podplane/bin/update-mutable-env.sh` script, and read only by the `/opt/podplane/bin/configure.sh` script. Contains for example the OIDC issuer URL. On `nst` (nstance-server) VMs, this file holds settings supplied at provisioning and is immutable for the VM's lifetime; changes require replacing the VM through its instance group.

4. `/opt/env/<service>.env`: mutable, updated only by the `/opt/podplane/bin/configure.sh` script, combining the outputs of `user-data.env` and `mutable.env` files.

The systemd services configured by `vmconfig` then source their respective `/opt/env/<service>.env` files to specify configuration; some are able to use env vars only, others have a helper script which passes those through to configuration flags.

### Example Configuration Flow for AWS

When you deploy a new cluster, configuration is propagated to each service like so:

```text
podplane cluster create
└─ writes podplane.cluster.jsonc
   └─ generates podplane.*.tf
      └─ calls Nstance Terraform modules
         └─ configures nstance-server groups/templates/files
         └─ AWS ASG scales up a VM which runs the nstance-server
            └─ nstance-server schedules new Podplane VMs e.g. of `knc` kind
               └─ cloud-init runs `user-data.sh` generated by Podplane
                  └─ user-data downloads and verifies all deps
                  └─ user-data extracts the /opt/deps/vmconfig.tar.gz archive
                  └─ user-data runs `install.sh` from /opt/podplane/bin
                  └─ user-data writes /opt/podplane/etc/user-data.env
                  └─ user-data runs `configure.sh` from /opt/podplane/bin
                     └─ runs `/opt/podplane/bin/bootstrap.sh` once
                        └─ creates /opt/podplane/etc/detected.env
                     └─ creates /opt/podplane/etc/mutable.env
                     └─ configure.sh generates /opt/env/*.env
                        ├─ /opt/env/nstance-agent.env
                        ├─ /opt/env/kubelet.env
                        ├─ /opt/env/kube-apiserver.env
                        ├─ /opt/env/netsy.env
                        └─ other component env files
                     └─ runs `/opt/podplane/bin/restart.sh` to start systemd services
               └─ systemd services source their /opt/env/<component>.env files   
```

When you update a cluster, for example to change the OIDC issuer, configuration is propagated using the lower half of the same pipeline:

```text
nstance-server runtime config/files change, sends a new `mutable.env` file to each node
└─ nstance-agent receives the `mutable.env` file into /opt/nstance-agent/recv
   └─ nstance-recv-watch.sh detects write-files.last
      ├─ update-mutable-env.sh validates + merges recv/mutable.env into /opt/podplane/etc/mutable.env
      ├─ copies delivered certs/keys/config files into their runtime locations
      ├─ runs configure.sh to regenerate /opt/env/*.env
      └─ restarts the affected services
```

### Mutable Configuration Vars

The following variables are able to be live-updated without having Nstance rotate VMs, via the `mutable.env` file as described above.

These variables can be set/overriden alongside the Podplane-generated `.tf` configuration:

| Environment variable | Generated OpenTofu/Terraform variable |
| --- | --- |
| `SSH_AUTHORIZED_KEYS` | `var.ssh_authorized_keys` |
| `TELEMETRY_ENABLED` | `tostring(var.telemetry_enabled)` |
| `TELEMETRY_LOG_CLOUDINIT` | `tostring(var.telemetry_log_cloudinit)` |
| `TELEMETRY_LOG_SERVICES` | `var.telemetry_log_services` |
| `TELEMETRY_OTLP_ENDPOINT` | `var.telemetry_otlp_endpoint` |
| `TELEMETRY_S3_BUCKET` | `var.telemetry_s3_bucket` |
| `TELEMETRY_S3_ENDPOINT` | `var.telemetry_s3_endpoint` |
| `TELEMETRY_S3_ACCESS_KEY_ID` | `var.telemetry_s3_access_key_id` |
| `TELEMETRY_S3_SECRET_ACCESS_KEY` | `var.telemetry_s3_secret_access_key` |
| `TELEMETRY_S3_ASSUME_ROLE` | `var.telemetry_s3_assume_role` |
| `OIDC_CA_CERT` | `var.oidc_ca_cert` |
| `OIDC_SIGNING_ALGS` | `join(",", var.oidc_signing_algs)` when non-null; vmconfig defaults to `RS256` |
| `KUBE_API_ETCD_SERVERS` | `var.kube_api_etcd_servers` |
| `KUBE_CLUSTER_CIDR` | `join(",", var.kubernetes_cluster_cidr)` |
| `KUBE_NODE_CIDR_MASK_SIZE_IPV4` | `tostring(var.kubernetes_node_cidr_mask_size_ipv4)` when non-null; vmconfig defaults to `24` |
| `KUBE_NODE_CIDR_MASK_SIZE_IPV6` | `tostring(var.kubernetes_node_cidr_mask_size_ipv6)` when non-null; vmconfig defaults to `64` |
| `KUBE_SERVICE_CLUSTER_IP_RANGE` | `join(",", var.kubernetes_service_cidr)` |
| `KUBE_LOG_LEVEL` | `tostring(var.kube_log_level)` |
| `AWS_S3_USE_PATH_STYLE` | `var.aws_s3_use_path_style` |
| `NETSY_ENDPOINT` | `var.netsy_endpoint` |
| `NETSY_ACCESS_KEY_ID` | `var.netsy_access_key_id` |
| `NETSY_SECRET_ACCESS_KEY` | `var.netsy_secret_access_key` |
| `REGISTRY_ENABLED` | `tostring(var.registry_enabled)` |
| `REGISTRY_ENDPOINT` | `var.registry_endpoint` |
| `REGISTRY_ACCESS_KEY_ID` | `var.registry_access_key_id` |
| `REGISTRY_SECRET_ACCESS_KEY` | `var.registry_secret_access_key` |
| `REGISTRY_HOSTNAME` | `var.registry_hostname` |

Prefer assume-role credentials over static access keys whenever the object store supports them. The generated static credential variables are marked sensitive to redact them from normal OpenTofu/Terraform output, but sensitive values are still stored in state. Use an encrypted, access-controlled state backend and avoid committing credential-bearing `.tfvars` files when static credentials are unavoidable.

The Podplane `.tf` configuration derives the following variables which can also be propagated via the `mutable.env` file:

| Environment variable | Generated OpenTofu/Terraform source |
| --- | --- |
| `TELEMETRY_S3_REGION` | `local.aws_region` |
| `OIDC_ISSUER` | `var.oidc_issuer_url` |
| `KUBE_API_PUBLIC_HOSTNAME` | `var.kubernetes_api_hostname` |
| `KUBE_API_INTERNAL_LB_HOSTNAME` | Internal Kubernetes API load balancer hostname, when generated for worker nodes |
| `KUBE_SERVICE_ACCOUNT_ISSUER` | `https://<var.kubernetes_api_hostname>:<var.kubernetes_api_port>` |
| `NETSY_BUCKET` | `aws_s3_bucket.podplane_cluster["netsy"].bucket` |
| `NETSY_REGION` | `local.aws_region` |
| `NETSY_ASSUME_ROLE` | `aws_iam_role.podplane_cluster["netsy"].arn` |
| `REGISTRY_BUCKET` | `aws_s3_bucket.podplane_cluster["registry"].bucket` |
| `REGISTRY_REGION` | `local.aws_region` |
| `REGISTRY_ASSUME_ROLE` | `aws_iam_role.podplane_cluster["registry-read-only"].arn` |

In the OpenTofu/Terraform configuration generated by the `cluster create` command, these values come from `local.mutable_env`.

Changing `KUBE_CLUSTER_CIDR` reconfigures kube-controller-manager's Pod CIDR setting. Changing `KUBE_SERVICE_CLUSTER_IP_RANGE` reconfigures the API server and controller manager's default Service range. These are control-plane process settings only: Podplane does not migrate existing Pods, Services, node CIDR allocations, CNI state, routes, or related networking state. Additional Kubernetes `ServiceCIDR` resources are separate and are not represented by `KUBE_SERVICE_CLUSTER_IP_RANGE`. The node CIDR mask variables control only new allocations. Changing them does not resize existing `Node.spec.podCIDRs` or migrate CNI state; existing allocations consume their corresponding blocks in the new allocation grid.

### Bootstrap Settings

Each instance VM kind has different bootstrap settings. Identity and agent registration inputs belong in `/opt/podplane/etc/user-data.env`; `nst` service settings are supplied in `/opt/podplane/etc/mutable.env` before the first configure run.

| Kind | Settings |
| --- | --- |
| `knd`, `knc` | `NSTANCE_EBPF_PORTS`: comma/space-separated production TCP target ports. NLB ingress uses `80,443`; tunnel ingress uses `443`; API uses `6443`; combined groups use the union. Empty/unset disables the counters and agent collection. |
| `nst` | Required: `NSTANCE_SERVER_SHARD`, `NSTANCE_SERVER_STORAGE`, `NSTANCE_SERVER_BUCKET`. Optional: `NSTANCE_SERVER_ID` (defaults to the detected cloud instance ID), `NSTANCE_SERVER_PREFIX`, `NSTANCE_SERVER_ADVERTISE_HOST` (defaults to the detected IPv4 address). Explicitly set `NSTANCE_SLEEP_ENABLED` and `NSTANCE_TUNNEL_ENABLED` to `true` or `false`. The sleep proxy runs while awake and asleep. |
| `nst` wake tunnels | When enabled, set `NSTANCE_TUNNEL_STORAGE`, `NSTANCE_TUNNEL_BUCKET`, and `NSTANCE_TUNNEL_SHARD` to the shard configuration location. Optional: `NSTANCE_TUNNEL_PREFIX`. The root supervisor independently resolves the tunnel configuration and files. |
| `nat` | Required: comma-separated `NAT_IPV4_SOURCE_CIDRS`. `NSTANCE_METRICS_INTERFACE` defaults to the detected interface; `NAT_CONNTRACK_MAX` defaults to `262144`. |
| `nat` NAT64 | `NAT64_ENABLED` defaults to `false`. Enabling requires `NAT64_SOURCE_IPV6_CIDRS` and `NAT64_POOL4_ADDRESS`. `NAT64_PREFIX` defaults to `64:ff9b::/96`; a custom `/96` requires matching `NAT64_BLOCKED_IPV6_CIDRS`. Jool modules must match the running kernel and architecture. Source allowlisting and non-global destination filtering precede translation. |

The `knd`, `knc`, and `nat` agents need registration/agent endpoints, a public CA,
and a nonce or existing identity. `nst` uses its own identity and runs no agent.
Activity counters are separate from Fluent Bit telemetry: vmconfig reports raw
cumulative values; Nstance owns rate/window decisions and sleep/wake ordering.

### Tunnel Configuration

Kubelet runs tunnel static Pods from `/etc/kubernetes/manifests`. On `knd`/`knc`,
vmconfig configures at most one production tunnel, owned by the VM lifecycle rather
than wake leases. The `nst` supervisor manages wake tunnels separately. Provider
resources, routes, credentials, and origin TLS settings belong to deployment
configuration; see [Kubernetes API & Ingress, NAT, and sleep](configuration.md#kubernetes-api-ingress-nat-and-sleep).

For a production tunnel, Nstance config defines `tunnel.env` in Nstance's
`templates.<name>.files` with `kind: "env"`. Values may be literal or use 
`{{ .Vars.KEY }}` references to template/group vars. Example delivered file:

```text
TUNNEL_ENABLED=true
TUNNEL_IMAGE=registry.example/tunnel@sha256:<64-hex-digest>
TUNNEL_ARGS=run|--config|/etc/tunnel/config.json
TUNNEL_FILES=tunnel-config.json:/etc/tunnel/config.json|tunnel-credentials.json:/etc/tunnel/credentials.json
```

Validation rules:

- `TUNNEL_ENABLED` must be `true` or `false`. Enabled images must be fully qualified and SHA-256-pinned.
- Arguments split on `|`; file mappings split on `|`, then the first `:`. Values remain literal, including spaces, quotes, backslashes, and `=`; no shell evaluation or quote removal occurs.
- Empty/omitted lists mean no arguments or mounts. Empty entries, embedded pipes, line breaks, and control characters are invalid.
- Sources must be regular `tunnel-*` basenames ending in `.env`, `.json`, `.crt`, `.pub`, or `.key`. Enabled bundles require all referenced files. Container mount paths must be unique, normalized, and absolute.
- Unknown/duplicate keys, unsafe paths, and symlinks are rejected before replacing the accepted configuration.
