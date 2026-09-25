---
title: "A local single-node cluster in around one minute"
linkTitle: "Local Dev Clusters"
weight: 70
description: "Run an offline Podplane development cluster locally, without a cloud account"
---

`podplane local start` creates a complete single-node cluster in a local QEMU VM.

Once dependencies are cached, it is designed to get from a stopped VM to a usable Kubernetes environment in around one minute.

```bash
podplane local start
```

The command downloads missing dependencies, starts the VM, waits for Netsy and the Kubernetes API, writes a kubeconfig context, and prints an example application deployment.

The default cluster includes the same core architecture used in production: containerd, kubelet, Netsy, Nstance agent, platform components, deployment templates, and a local registry. It's design to use to same configuration as a production VM for environment parity, to give you greater confidence in your tests.

Local clusters are for development and testing, not production. They use one VM, a fixed local test identity, and local files for durable data. 

Use `--cpus` and `--memory` to adjust resources, and use named cluster IDs when you need more than one environment.

Podplane runs a local background server which provides local-only implementations of the external services a cluster needs, including OIDC, S3-compatible object storage storage, OpenBao-compatible secrets backend, and Nstance server for VM registration and certificate issuance. This keeps the local VM self-contained while using the same APIs/interfaces as a VM deployed in production.

## Learn more

- [`podplane local start` reference](../reference/cli/commands/local-start.md)
- [Local command reference](../reference/cli/commands/_index.md#local-commands)
- [VM configuration](../reference/vm-configuration.md)
