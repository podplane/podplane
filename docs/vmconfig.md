---
title: "VM Configuration"
weight: 40
description: "How Podplane configures VMs using vmconfig"
---

# VM Configuration

The "VM Layer" of the Podplane [Architecture](architecture.md) is responsible for getting Pods scheduled on a VM. This is essentially Netsy + core Kubernetes + containerd, configured by [vmconfig](https://github.com/podplane/vmconfig) - a minimal configuration system designed for Debian-based Linux VMs, written in Bash.

## How It Works

The userdata script which invokes the `vmconfig` entrypoint is responsible for supplying the package dependencies, which is defined by the "kind" of VMs you want `vmconfig` to configure:

1. `knd` creates a Kubernetes Data Plane / Worker node, which runs kubelet, containerd, and supporting services.

2. `knc` creates a Kubernetes Control Plane node, which is essentially a base of `knd` + adds Netsy (as an etcd alternative), kube-apiserver, kube-scheduler, kube-controller-manager, and a stateless container registry.

## Deployed Clusters

The flow for clusters you create via Podplane CLI is:

1. CLI generates and deploys infrastructure-as-code
2. Nstance auto-scales VMs using the Podplane userdata script
3. Each VM's userdata script downloads the relevant packages and runs the vmconfig entrypoint to configure the VM
4. vmconfig sets up all required services for the VM's kind (`knd` = data plane, `knc` = control plane)
5. Control Plane nodes run Netsy backed by object storage, replacing the need for etcd and on-disk state

## Local Clusters

For local VMs run via the Podplane CLI, the same `vmconfig` configuration is used to run a single `knc` VM per local cluster.

The CLI itself is responsible for downloading/caching package dependencies and serving them to the VM via a webserver it runs in the background via the [local start](cli-reference/local-start.md) command. You can also use [package server](cli-reference/package-server.md) to run the webserver directly.

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
