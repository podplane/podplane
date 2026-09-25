---
title: "Podplane on Proxmox VE"
linkTitle: "Proxmox"
weight: 30
description: "Run the Podplane VM lifecycle on your own hardware"
---

Nstance and vmconfig support Proxmox VE as an infrastructure provider. Nstance can create and remove VMs through the Proxmox API, while the same Podplane bootstrap process configures those VMs as Kubernetes nodes.

This is useful when you want to run Podplane on hardware you control while keeping the same node lifecycle and Kubernetes platform used in public cloud environments.

## What you provide

Proxmox support is currently a runtime foundation rather than a complete `podplane cluster create` workflow. A more automated experience will be implemented once Google Cloud support is finished.

Today, you are responsible for the surrounding infrastructure, including:

- Proxmox templates, API credentials, networking, and address allocation.
- S3-compatible object storage with the conditional-write behavior Netsy requires.
- Load balancing or a virtual IP for the Kubernetes API and application ingress.
- DNS, secrets storage, and any external identity provider.
- Infrastructure code or automation that connects these services to Nstance and vmconfig.

Proxmox does not have a Spot or preemptible purchasing model, so Nstance's interruption monitoring is disabled for this provider. Health replacement, desired-capacity reconciliation, and normal VM lifecycle management still apply.

## Learn more

- [Cloud provider support](./_index.md)
- [Nstance Proxmox provider](https://github.com/nstance-dev/nstance/blob/main/docs/providers/proxmox.md)
- [VM configuration](../../reference/vm-configuration.md)
- [Object-backed cluster state](../object-backed-cluster-state.md)
