---
title: "Pods sandboxed with gVisor by default"
linkTitle: "Sandboxed Workloads"
weight: 35
description: "Run every Pod in its own gVisor sandbox, with runc available where an admin allows it"
---

Containers normally share the node's Linux kernel, so a kernel exploit in one
container can reach the node and everything else on it. Podplane runs Pods in
[gVisor](https://gvisor.dev/) by default, which gives each Pod its own
application kernel. Apps don't need any changes to use it.

Workloads that need the node's kernel directly can run under runc instead, but
only in namespaces a cluster admin has allowed. Being able to deploy into a
namespace is never enough to opt out of the sandbox.

Admission policies apply these rules to every new Pod, including Pods created by
controllers, and reject unsupported runtimes rather than falling back to runc.

## Learn more

- [Container Runtimes guide](../guides/container-runtimes.md)
- [Pod Security guide](../guides/pod-security.md)
- [gVisor documentation](https://gvisor.dev/docs/)
