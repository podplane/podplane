---
title: "Run Podplane on your infrastructure"
linkTitle: "Cloud Providers"
weight: 130
description: "Run Podplane on your infrastructure - whether it's public cloud, private cloud, on-prem, or locally"
---

Podplane is designed to run the same way everywhere: VMs managed by [Nstance](https://nstance.dev), durable state in object storage via [Netsy](https://netsy.dev), OIDC for authentication, and standard Kubernetes APIs above them.

The level of end-to-end automation currently differs by provider:

- [AWS](aws.md) has the complete `podplane cluster create` workflow, including generated OpenTofu/Terraform infrastructure.

- [Google Cloud](google-cloud.md) is supported by Nstance, Netsy, vmconfig, and provider-specific dependency manifests, but is not yet a first-class `podplane cluster create` target - this is coming soon.

- [Proxmox](proxmox.md) is supported by Nstance and vmconfig for VM lifecycle and bootstrap, but requires external object storage, networking integration, and manual infrastructure orchestration today - this is coming next.

## Shared architecture

Across providers, Podplane keeps the same model:

1. Nstance creates, replaces, and drains VMs.
2. `vmconfig` turns a base Debian VM into a Podplane node.
3. Netsy stores durable Kubernetes state in object storage.
4. Kubernetes components and Podplane addons run the same workloads above the infrastructure layer.

See the provider pages for the integrations and responsibilities specific to each environment.
