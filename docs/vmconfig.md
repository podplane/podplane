---
title: "VM Configuration"
weight: 40
description: "How Podplane configures VMs using vmconfig"
---

# VM Configuration

The "VM Layer" of the Podplane [Architecture](architecture.md) is responsible for getting Pods scheduled on a VM. This is essentially Netsy + core Kubernetes + containerd, configured by [vmconfig](https://github.com/podplane/vmconfig) - a minimal configuration system designed for Debian-based Linux VMs, written in Bash.

## How It Works

The user-data script which invokes the `vmconfig` entrypoint is responsible for supplying the package dependencies, which is defined by the "kind" of VMs you want `vmconfig` to configure:

1. `knd` creates a Kubernetes Data Plane / Worker node, which runs kubelet, containerd, and supporting services.

2. `knc` creates a Kubernetes Control Plane node, which is essentially a base of `knd` + adds Netsy (as an etcd alternative), kube-apiserver, kube-scheduler, kube-controller-manager, and a stateless container registry.

## Deployed Clusters

The flow for clusters you create via Podplane CLI is:

1. CLI generates and deploys infrastructure-as-code
2. Nstance auto-scales VMs using the Podplane-generated cloud-init user-data script
3. Each VM's user-data script downloads the relevant dependencies, extracts the `vmconfig` archive, and runs its install/configure scripts
4. vmconfig sets up all required services for the VM's kind (`knd` = data plane, `knc` = control plane)
5. Control Plane nodes run Netsy backed by object storage, replacing the need for etcd and on-disk state

## Local Clusters

For local VMs run via the Podplane CLI, the same `vmconfig` configuration is used to run a single `knc` VM per local cluster.

The CLI itself is responsible for downloading/caching dependencies and serving them to the VM via a webserver it runs in the background via the [local start](cli-reference/local-start.md) command. The same webserver also hosts a fake OIDC server and fake S3 server for local clusters. You can also use [local server](cli-reference/local-server.md) to run the webserver directly.

## Container Registry

VMs run a Zot Registry backed by the configured registry object-storage bucket.

Podplane supports component and template image "mirroring" whereby the registry bucket stores a copy of all required container images if mirroring is enabled.

To use this mirror, components render explicit image references such as `<registry-hostname>/mirror/<original-registry>/<repository>:<tag>`. This differs to user-pushed app images which live under `<registry-hostname>/apps/...`. Podplane intentionally does not make zot a transparent containerd pull-through cache for all image pulls - while that would have made configuring image references easier without the `<registry-hostname>/` prefix, the decision to use explicit references was because:

1. it is obvious from the rendered Kubernetes manifest when an image is using the Podplane built-in registry, even though components and templates need to do more work to render the correct image references (e.g. you have to override an off-the-shelf Helm chart values file to change the images used)
2. user workloads keep native Kubernetes registry authentication behavior, including per-namespace and per-service-account `imagePullSecrets`; a transparent zot pull-through cache would require zot to authenticate to upstream registries itself and would not naturally receive the pod's upstream registry credentials

Templates and components should therefore use canonical upstream image references by default, and only render mirrored references intentionally when Podplane owns the image selection and registry mirror behavior.

## Package Dependencies

__Data Plane & Control Plane VMs__:
- [nstance-agent](https://nstance.dev/docs/components/nstance-agent/) to register VMs
- [fluent-bit](https://docs.fluentbit.io/manual) for log forwarding
  - [libpq5](https://packages.debian.org/trixie/libpq5) runtime dependency for fluent-bit
- [kube2iam](https://github.com/jtblin/kube2iam) for providing IAM Roles to pods
- [containerd](https://containerd.io/docs/main/) the container runtime 
- [runc](https://github.com/opencontainers/runc) for containerd to spawn OCI containers
- [kubelet](https://kubernetes.io/docs/reference/command-line-tools-reference/kubelet/) for running node pods
  - [uidmap](https://packages.debian.org/sid/uidmap) runtime dependency of kubelet for getsubids
  - [libsubid5](https://packages.debian.org/trixie/libsubid5) runtime dependency of kubelet for getsubids
- [cni-plugins](https://github.com/containernetworking/plugins) the reference CNI plugins

__Control Plane VMs__:
- [netsy](https://netsy.dev/docs/design/) as an etcd alternative
- [kube-apiserver](https://kubernetes.io/docs/reference/command-line-tools-reference/kube-apiserver/) the Kubernetes API server
- [kube-scheduler](https://kubernetes.io/docs/reference/command-line-tools-reference/kube-scheduler/) the Kubernetes scheduler
- [kube-controller-manager](https://kubernetes.io/docs/reference/command-line-tools-reference/kube-controller-manager/) the core Kubernetes control loops
- [distribution](https://distribution.github.io/distribution/) the stateless container registry

## Env Vars & Service Configuration

While most of the configuration for each VM kind is the same, there are a set of variable inputs to the `vmconfig` package, propagated to each service via environment variable `.env` files:

1. `/opt/podplane/etc/user-data.env`: immutable, created on first-boot by the cloud-init `user-data.sh` script, read only by the `/opt/podplane/bin/configure.sh` script. Contains the instance identity and minimum configuration needed to register nstance-agent. The Nstance registration nonce and CA certificate are written to separate identity files. It can also contain `IMMUTABLE_SSH_AUTHORIZED_KEYS` for debugging failures before nstance-agent becomes healthy; note that changing or removing those keys requires rotating affected VMs, so the `SSH_AUTHORIZED_KEYS` mutable alternative is recommended for general SSH access.

2. `/opt/podplane/etc/detected.env`: immutable, created during the first run of the `/opt/podplane/bin/bootstrap.sh` script, and read only by the `/opt/podplane/bin/configure.sh` script. Contains for example the detected instance hostname and IPv4 / IPv6 addresses.

3. `/opt/podplane/etc/mutable.env`: mutable, created during the first run of the `/opt/podplane/bin/bootstrap.sh` script, able to be updated by the `/opt/podplane/bin/update-mutable-env.sh` script, and read only by the `/opt/podplane/bin/configure.sh` script. Contains for example the OIDC issuer URL.

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
| `KUBE_API_ETCD_SERVERS` | `var.kube_api_etcd_servers` |
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

The Podplane `.tf` configuration derives the following variables which can also be propagated via the `mutable.env` file:

| Environment variable | Generated OpenTofu/Terraform source |
| --- | --- |
| `TELEMETRY_S3_REGION` | `local.aws_region` |
| `OIDC_ISSUER` | `local.oidc_issuer_url` |
| `KUBE_API_PUBLIC_HOSTNAME` | `local.kubernetes_api_hostname` |
| `KUBE_API_INTERNAL_LB_HOSTNAME` | Internal Kubernetes API load balancer hostname, when generated for worker nodes |
| `KUBE_API_PORT` | `tostring(local.kubernetes_api_port)` |
| `NETSY_BUCKET` | `aws_s3_bucket.netsy.bucket` |
| `NETSY_REGION` | `local.aws_region` |
| `NETSY_ASSUME_ROLE` | `aws_iam_role.netsy.arn` |
| `REGISTRY_BUCKET` | `aws_s3_bucket.registry.bucket` |
| `REGISTRY_REGION` | `local.aws_region` |
| `REGISTRY_ASSUME_ROLE` | `aws_iam_role.registry_read_only.arn` |

In the OpenTofu/Terraform configuration generated by the `cluster create` command, these values come from `local.mutable_env`.
