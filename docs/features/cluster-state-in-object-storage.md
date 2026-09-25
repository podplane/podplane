---
title: "Object Storaged backed Kubernetes cluster state with Netsy"
linkTitle: "Cluster State in Object Storage"
weight: 100
description: "Use durable object storage instead of requiring an operationally-intensive multi-node stateful etcd cluster"
---

Podplane uses [Netsy](https://netsy.dev) as the Kubernetes API server's etcd-compatible cluster state service.

Object storage is the durable system of record, while each control-plane node keeps a disposable local SQLite copy for fast reads and coordination.

This changes the traditional operational model of Kubernetes, as cluster state is not tied to a particular VM or persistent disk.

A replacement control-plane node can rebuild its local state from the object store, and single-node clusters do not need a separate etcd quorum or disk volume.

In a healthy multi-node cluster, Netsy can acknowledge a transaction after a durable replica quorum and flush records to object storage asynchronously. If there are not enough healthy replicas, including in a single-node cluster, it falls back to synchronous object-storage writes.

Netsy supports Amazon S3, Google Cloud Storage, and compatible S3 services with the conditional-write behavior it requires. Persistent local disks are optional and only improve restart speed; they are not the system of record.

## Netsy Snapshots/Seed Mechanism

Podplane creates the initial Netsy snapshot from a versioned "seed" file. The snapshot is uploaded to AWS or Google Cloud object storage using a conditional write and never overwrites an existing cluster, protecting durable state when infrastructure is reapplied.

## Learn more

- [Podplane architecture](../reference/architecture.md)
- [Seed files](../reference/seeds.md)
- [Netsy storage and replication](https://github.com/netsy-dev/netsy/blob/main/docs/design/storage-replication.md)
