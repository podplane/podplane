---
title: "Cluster Auto-Scaling with Nstance"
linkTitle: "Cluster Auto-Scaling"
weight: 110
description: "Automatically adjust cluster node capacity with the Kubernetes cluster-autoscaler and Cluster API integration, and handle on-demand and spot instances"
---

Podplane uses [Nstance](https://nstance.dev) to manage cluster VMs.

Nstance continuously reconciles each managed group to its desired capacity, creates missing instances, removes excess capacity, and replaces unhealthy or expired nodes.

Nstance integrates with the Kubernetes cluster-autoscaler via the Cluster API (CAPI).

Configuration changes that affect a VM — such as its machine image, instance type, architecture, or bootstrap data — also flow through Nstance. It rolls nodes one at a time instead of treating the cluster as a fixed set of machines.

## On-demand and spot capacity

Nstance supports two useful kinds of dynamic capacity:

- **On-demand nodes** are created for an annotated Pod in addition to a group's normal desired size. They remain associated with that workload rather than being counted as ordinary group capacity.

- **Spot and preemptible nodes** use provider-specific provisioning settings. The Nstance agent detects AWS Spot and Google preemption notices, asks for replacement capacity, and coordinates draining the interrupted node.

Note: "On-Demand" here describes Nstance's auto-creation of a Kubernetes Node from an annotated Pod; it is not the same thing as the AWS On-Demand purchasing model.

## Learn more

- [Infrastructure configuration impact](../reference/infrastructure.md#configuration-impact)
- [Nstance auto-scaling](https://github.com/nstance-dev/nstance/blob/main/docs/features/auto-scaling.md)
- [Nstance on-demand nodes](https://github.com/nstance-dev/nstance/blob/main/docs/features/on-demand-nodes.md)
- [Nstance spot instances](https://github.com/nstance-dev/nstance/blob/main/docs/features/spot-instances.md)
