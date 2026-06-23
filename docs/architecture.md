---
title: "Architecture"
weight: 20
description: "Overview of the Podplane architecture and components"
---

# Podplane Architecture

Podplane aims to make container infrastructure easy, and uses a unique Kubernetes-based platform architecture to achieve this goal.

## What Makes Podplane Different?

Podplane is easy to use and operate because it combines three sibling projects into a new type of Kubernetes-based container platform:

- Cluster state is stored in object storage via [Netsy](https://netsy.dev), not on disk via etcd.
- Auto-scaling & provisioning is faster with [Nstance](https://nstance.dev).
- OIDC & RBAC is simplified with [Easy OIDC](https://easy-oidc.dev) (or you can BYO existing OIDC servers)

Podplane itself consists of three key components:

1. [podplane CLI](https://github.com/podplane/podplane): a CLI for deploying and managing clusters, written in Go.
2. [vmconfig](https://github.com/podplane/vmconfig): a minimal configuration system designed for Debian-based Linux VMs, written in Bash.
3. [components](https://github.com/podplane/components): a collection of Helm charts used to seed the Kubernetes cluster state.

Podplane, Netsy, Nstance, and Easy OIDC are Open Source projects created by [Nadrama](https://nadrama.com).

## Platform Layers

A Podplane cluster consists of three platform layers:

1. __Infrastructure Layer__: gets VMs scheduled. This is largely infrastructure-as-code (OpenTofu/Terraform) + Nstance.

2. __Virtual Machine (VM) Layer__: gets Pods scheduled on a VM. This is essentially Netsy + core Kubernetes + containerd.

3. __Container Layer__: delivers a working Developer Platform for devs. This is where Podplane components run atop Kubernetes, e.g. CNI or ingress.

## Component Overview

### Infrastructure & VM Layer

```
┌────────────┐    ┌──────────────┐
│Podplane CLI├───▶│OpenTofu / TF │
└────────────┘    └──────┬───────┘
                         │ 
┌────────────────────────▼─────────────────────────────────────────┐
│              Provider (AWS / Google Cloud / Proxmox)             │
│                                                                  │
│     ┌───────────────────────┐   ┌──────────────────┐             │
│     │  nstance-server (VM)  │◀──│  Auto-Scaling    │             │
│     └──▲─────┬──────────────┘   │  Groups (ASGs)   │             │
│        │     │ manages          └──────────────────┘             │
│     ┌──┼─────▼─────────────────────────────┐  ┌───────────────┐  │
│     │  │       VM Instances                │  │ Object Storage│  │
│     │ ┌┴──────────────┐ ┌────────────────┐ │  └──▲────▲────▲──┘  │
│     │ │ nstance-agent │ │ fluent-bit     ├─┼─────┘    │    │     │
│     │ ├───────────────┤ ├────────────────┤ │          │    │     │
│     │ │ kube2iam      │ │ distribution   ├─┼──────────┘    │     │
│     │ ├───────────────┤ ├────────────────┤ │               │     │
│     │ │ kubelet       │ │ netsy          ├─┼───────────────┘     │
│     │ ├───────────────┤ ├────────────────┤ │                     │
│     │ │ containerd    │ │ kube-scheduler │ │                     │
│     │ ├───────────────┤ ├────────────────┤ │                     │
│     │ │ runc          │ │ kube-ctrl-mgr  │ │                     │
│     │ ├───────────────┤ ├────────────────┤ │  ┌───────────────┐  │
│     │ │ cni-plugins   │ │ kube-apiserver │ │  │   Easy OIDC   │  │
│     │ └───────────────┘ └──▲──────────┬──┘ │  │     server    │  │
│     └──────────────────────┼──────────│────┘  └───▲────────▲──┘  │
│                            │          └───────────┘        │     │
└────────────────────────────┼───────────────────────────────┼─────┘
                             │      ┌─────────────────┐      │
                             └──────│   Developers    ├──────┘
                                    │   (kubectl)     │    login
                                    └─────────────────┘
```

The sequence of how these all fit together is:

1. `Podplane CLI` generates infrastructure-as-code configuration files

2. If you don't have an existing OIDC server, the CLI can deploy an `Easy OIDC` server for you.

3. OpenTofu/Terraform deploys infrastructure (on AWS/Google Cloud)

4. Podplane bootstraps cluster state using configuration generated by [components](https://github.com/podplane/components)

5. `Nstance` auto-scales cluster VMs using the Podplane userdata script

6. Each VM userdata script downloads the relevant packages, and runs the `vmconfig` package entrypoint to configure the VM.

7. Control Plane nodes run `Netsy` as an etcd-alternative, and all standard Kubernetes components such as `kube-apiserver`

8. Developers use `podplane login` to authenticate with your cluster

9. Developers can use `podplane deploy` to easily deploy apps using templates

   - When using the `deploy` command, the CLI will prompt to automatically `podplane install` required components like cert-manager and Traefik if not already present

## Learn More

For detailed information about each layer, see:

- [Infrastructure](../infrastructure.md) - how Podplane provisions and manages cloud infrastructure.
- [VM Configuration](../vmconfig.md) - how VMs are configured and what runs on them.
- [Components](../components.md) - the component system, including core components and addon installation.
- [Secrets](../secrets.md) - how Podplane manages and mounts application secrets.
