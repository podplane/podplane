---
title: "Install Podplane"
weight: 10
description: "Install the Podplane CLI and the tools needed for local and cloud clusters"
---

Podplane provides CLI releases for macOS, Windows, and Linux on `amd64` and `arm64`.

## Install the CLI

On macOS with [Homebrew](https://brew.sh/):

```bash
brew install podplane/tap/podplane
```

Or on macOS/Linux/Windows with [Go](https://go.dev/):

```bash
go install github.com/podplane/podplane@latest
```

Confirm the CLI is available:

```bash
podplane version
```

## Install dependencies

Install [kubectl](https://kubernetes.io/docs/tasks/tools/) for Kubernetes access and [Helm](https://helm.sh/docs/intro/install/) for application deployments and component management.

For a local cluster, also install:

- [QEMU](https://www.qemu.org/download/) with `qemu-img` and the system binary for your architecture.
- [mkcert](https://github.com/FiloSottile/mkcert), then (optionally) run `mkcert -install` so local HTTPS certificates are trusted.

The local VM workflow currently targets Apple silicon (`darwin/arm64`) and x86-64 Linux (`linux/amd64`).

For an AWS cluster, install either [OpenTofu](https://opentofu.org/docs/intro/install/) or [Terraform](https://developer.hashicorp.com/terraform/install), and configure AWS credentials or a profile that can create the required infrastructure. Podplane prefers `tofu` when both engines are installed.

You are now ready to [start a local cluster](local-cluster.md) or [create an AWS cluster](aws-cluster.md).
