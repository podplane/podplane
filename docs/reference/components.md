---
title: "Components"
weight: 40
description: "How Podplane manages cluster components"
aliases:
  - /docs/components/
---

# Components

The "Containers Layer" of the Podplane [Architecture](architecture.md) is designed as a set of "Components", categorised into:

1. __Core Components__ required for minimal cluster operation, so you can deploy and schedule Pods.

2. __Addon Components__ for optional functionality to extend cluster capabilities - things like an ingress controller, CSI drivers, and more.

During cluster creation, users are given three initial state options to choose from:

1. __Recommended__ - which includes all Core Components + a small selection of commonly used Addon Components such as Envoy Gateway. The goal is that deployment templates such as "web" do not require any additional Addon Components to be installed.

2. __Minimal__ - deploys just the Core Components. From there, users can manually install Components using `podplane install` as required.

3. __None__ - does not install any Podplane Components, meaning you get a bare Kubernetes cluster and Nodes will be `NotReady` until a CNI is installed and they can become `Ready` and able to schedule Pods. For advanced users only.

Components are deployed with an opinionated, tested configuration - not the full surface area of each component's underlying official Helm chart.

The current component charts require Kubernetes 1.37 or later. This is also the minimum version for the stable Pod Certificates and ClusterTrustBundles used by Podplane's workload certificate system.

### Core Components

- `coredns` for cluster DNS
- `cilium` for cluster CNI
    - `cilium-crds` for Cilium CNI
- `fluxcd` for automated Podplane container-layer upgrades
    - `fluxcd-crds` for Flux CD
- `gateway-api-crds` for ingress controllers using Gateway API
- `platform-components` for Podplane component management. This chart creates the Flux source, platform namespaces, and HelmReleases for enabled components.
- `platform-rbac` for default Podplane platform [RBAC](../guides/rbac.md) and admission policies

### Provider-Specific Components

These charts are marked as provider-specific core components and are enabled by cluster bootstrap configuration when required:

- `csi-aws-ebs` for persistent storage on AWS
- `cluster-api` for the [Cluster API](https://cluster-api.sigs.k8s.io/) core controller on AWS and Google Cloud
    - `cluster-api-crds`
- `nstance-operator` for the [Nstance Operator](https://nstance.dev/docs/components/nstance-operator/) on AWS and Google Cloud (requires `cluster-api`)
    - `nstance-operator-crds`

### Addon Components

Recommended components which can also be installed via `podplane install` atop the Minimal set:

- `agent-sandbox`: [Agent Sandbox](https://agent-sandbox.sigs.k8s.io/) controller for isolated, stateful singleton workloads such as AI agent runtimes
    - `agent-sandbox-crds`
- `podplane-operator` for Podplane platform APIs and controllers. It signs workload and Service certificates, publishes the workload trust bundle, injects that CA into annotated API extension resources, and manages self-signed fallback and optional ACME ingress certificates.
    - `podplane-operator-crds`
- `secrets-store-csi-driver`: [Secrets Store CSI Driver](https://secrets-store-csi-driver.sigs.k8s.io/) for mounting provider-backed secrets into Pods. Provider-specific components are installed separately based on cluster configuration.
    - `secrets-store-csi-driver-crds`
- `envoy-gateway`: [Envoy Gateway](https://gateway.envoyproxy.io/) ingress controller
    - `envoy-gateway-crds`
- `zot-registry` for the in-cluster OCI registry

Addon components which can only be installed via `podplane install`:

- `snapshot`: [Snapshot controller](https://kubernetes-csi.github.io/docs/snapshot-controller.html)
    - `snapshot-crds`
- `metrics-server`: [Kubernetes Metrics Server](https://github.com/kubernetes-sigs/metrics-server)
- `cluster-autoscaler`: [Kubernetes Cluster Autoscaler](https://github.com/kubernetes/autoscaler/tree/master/cluster-autoscaler) for automatic node scaling via Cluster API
- `node-problem-detector`: [Node Problem Detector](https://github.com/kubernetes/node-problem-detector) for surfacing node hardware/kernel/runtime issues

## How It Works

### Cluster State Initialization

Cluster state is initialised via the Podplane provider for OpenTofu/Terraform.

The provider uses Podplane seed files to create a Netsy `bootstrap.netsy` snapshot file, then uploads it to provider-specific object storage (S3 for AWS, GCS for Google Cloud). Before uploading, the provider checks that remote state does not already exist and performs a conditional put so it can never overwrite it by mistake. Netsy also has checks to ensure a bootstrap file is never loaded over existing cluster state.

The `Recommended` and `Minimal` options each have their own Podplane seed files. The `None` option skips cluster seeding entirely.

See [Seeds](seeds.md) for how seed files, the seeds manifest, the `seedgen` seed generator utility, and the Terraform provider fit together.

### The Platform Components Chart

The core `platform-components` chart is the control point for component installation. It defines the available app and CRD components, their dependencies, namespaces, image-mirror settings, and per-component values.

Its values are the canonical in-cluster list of enabled components and their configuration. Flux CD watches the chart and reconciles HelmRelease resources for each enabled component:

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
          "semver": "^1.2.3"
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
- App templates (used by `podplane deploy`) also have component dependencies (e.g. the `web` template requires Envoy Gateway).

When running `podplane deploy` or `podplane install`, the CLI checks dependencies and prompts the user to install missing ones.

### Relationship to Cluster Config

`podplane.cluster.jsonc` is the user-facing source for cluster identity, initial seed selection, component source, image-mirror settings, and platform features that affect generated component values.

Initial component enablement comes from the selected seed. Cluster creation interpolates relevant cluster settings into the `platform-components` values, while later `podplane install` and `podplane uninstall` operations update the in-cluster HelmRelease. The cluster is the runtime source of truth for which addons are enabled.
