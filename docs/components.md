---
title: "Components"
weight: 50
description: "How Podplane manages cluster components"
---

# Components

The "Containers Layer" of the Podplane [Architecture](architecture.md) is designed as a set of "Components", categorised into:

1. __Core Components__ required for minimal cluster operation, so you can deploy and schedule Pods.

2. __Addon Components__ for optional functionality to extend cluster capabilities - things like an ingress controller, CSI drivers, and more.

During cluster creation, users are given three initial state options to choose from:

1. __Recommended__ - which includes all Core Components + a small selection of commonly used Addon Components such as Traefik ingress controller. The goal is that deployment templates such as "web" do not require any additional Addon Components to be installed.

2. __Minimal__ - deploys just the Core Components. From there, users can manually install Components using `podplane install` as required.

3. __None__ - does not install any Podplane Components, meaning you get a bare Kubernetes cluster and Nodes will be `NotReady` until a CNI is installed and they can become `Ready` and able to schedule Pods. For advanced users only.

Components are deployed with an opinionated, tested configuration - not the full surface area of each component's underlying official Helm chart.

### Core Components

- `coredns` for cluster DNS
- `cilium` for cluster CNI
    - `cilium-crds` for Cilium CNI
- `fluxcd` for automated Podplane container-layer upgrades
    - `fluxcd-crds` for Flux CD
- `gateway-api-crds` for any ingress controller using Gateway API, particularly Traefik
- `platform-components` for Podplane component management. This chart creates the Flux source, platform namespaces, and HelmReleases for enabled components.
- `platform-rbac` for default Podplane platform RBAC and admission policies

### Addon Components

Recommended components which can also be installed via `podplane install` atop the Minimal set:

- `agent-sandbox`: [Agent Sandbox](https://agent-sandbox.sigs.k8s.io/) controller for isolated, stateful singleton workloads such as AI agent runtimes
    - `agent-sandbox-crds`
- `cluster-api` for the [Cluster API](https://cluster-api.sigs.k8s.io/) core controller
    - `cluster-api-crds`
- `nstance` for the [Nstance Operator](https://nstance.dev/docs/components/nstance-operator/) (requires `cluster-api`)
    - `nstance-crds`
- `cert-manager`: [cert-manager](https://cert-manager.io/docs/) and [cert-manager-csi-driver](https://cert-manager.io/docs/usage/csi-driver/)
    - `cert-manager-crds`
- `platform-certs` for default self-signed and ACME certificate issuers, CA, certificates, etc. (requires `cert-manager`)
- `trust-manager`: [trust-manager](https://cert-manager.io/docs/trust/trust-manager/) by the cert-manager project
    - `trust-manager-crds`
- `platform-trust` for default trust bundles (requires `trust-manager`)
- `podplane-operator` for Podplane platform APIs and controllers such as Secrets
    - `podplane-operator-crds`
- `secrets-store-csi-driver`: [Secrets Store CSI Driver](https://secrets-store-csi-driver.sigs.k8s.io/) for mounting provider-backed secrets into Pods. The recommended set includes the OpenBao provider; other provider-specific components, such as AWS, GCP, and Vault providers, are installed separately based on cluster/provider needs.
    - `secrets-store-csi-driver-crds`
    - `secrets-store-csi-provider-openbao`
- `traefik`: [Traefik](https://doc.traefik.io/traefik/) ingress controller

Addon components which can only be installed via `podplane install`:

- `snapshot`: [Snapshot controller](https://kubernetes-csi.github.io/docs/snapshot-controller.html)
    - `snapshot-crds`
- Cloud Provider CSI Drivers:
    - `csi-aws-ebs`: [AWS EBS CSI Drivers](https://github.com/kubernetes-sigs/aws-ebs-csi-driver)
- `metrics-server`: [Kubernetes Metrics Server](https://github.com/kubernetes-sigs/metrics-server)
- `cluster-autoscaler`: [Kubernetes Cluster Autoscaler](https://github.com/kubernetes/autoscaler/tree/master/cluster-autoscaler) for automatic node scaling via Cluster API
- `node-problem-detector`: [Node Problem Detector](https://github.com/kubernetes/node-problem-detector) for surfacing node hardware/kernel/runtime issues

## How It Works

### Cluster State Initialization

Cluster state is initialised via the Podplane provider for OpenTofu/Terraform.

The provider uses Podplane seed files to create a Netsy `bootstrap.netsy` snapshot file, then uploads it to provider-specific object storage (S3 for AWS, GCS for Google Cloud). Before uploading, the provider checks that remote state does not already exist and performs a conditional put so it can never overwrite it by mistake. Netsy also has checks to ensure a bootstrap file is never loaded over existing cluster state.

The `Recommended` and `Minimal` options each have their own Podplane seed files. The `None` option skips cluster seeding entirely.

See [Seeds](seeds.md) for how seed files, the seeds manifest, the `seedgen` seed generator utility, and the Terraform provider fit together.

### The Platform Component

A core component called `platform` is a Helm chart that holds all Podplane-related configuration - reserved namespaces, components management, default trust bundles (enabled when trust-manager is installed), etc.

It acts as the single control point for all component installations. The platform chart's values file is the canonical list of enabled components and their configuration. Flux CD watches the platform chart and reconciles HelmRelease resources for each enabled component:

- `podplane install <component>` updates the platform chart values to enable a component; Flux CD then deploys it.
- `podplane uninstall <component>` disables a component in the platform chart values; Flux CD removes it.
- Core components cannot be uninstalled.

By default, Flux sources component charts from the published Podplane [components](https://github.com/podplane/components) repository. For private or forked components repositories, configure the Flux source directly in cluster config and create the referenced Kubernetes Secret (per `source.secretRef.name` below) in the `platform-components` namespace:

```jsonc
{
  "cluster": {
    "components": {
      "source": {
        "url": "https://github.com/acme/components.git",
        "ref": {
          "semver": "v1.2.3-acme.1"
        },
        "secretRef": {
          "name": "components-git-auth"
        }
      }
    }
  }
}
```

### Component Dependencies

Component dependency metadata lives in the platform chart within the cluster itself - not hardcoded in the CLI. The CLI queries the Kubernetes API to read the platform chart's metadata to determine what's installed and what dependencies exist.

This means the CLI doesn't need to bundle or fetch dependency information from the [components](https://github.com/podplane/components) repo at runtime. The `components` repo is where charts are authored, but the cluster is the runtime source of truth.

Dependency examples:

- Some addon components depend on other addon components (e.g. snapshot requires snapshot-crds).
- App templates (used by `podplane deploy`) also have component dependencies (e.g. the `web` template requires traefik).

When running `podplane deploy` or `podplane install`, the CLI checks dependencies and prompts the user to install missing ones.

### Relationship to Cluster Config

`podplane.cluster.jsonc` is the user-facing projection of cluster configuration, including configuration values like cluster name/slug, and which components and features are enabled.

Conceptually, the cluster config file syncs its component/feature settings into the platform chart's values.
