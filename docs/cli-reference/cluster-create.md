---
title: "cluster create"
weight: 10
description: "Generate cluster configuration and deploy infrastructure"
---

## Overview

Generates or reads a cluster config file, generates infrastructure-as-code files, and (for AWS/Google Cloud) deploys the cluster via OpenTofu/Terraform. When creating a new config interactively, the command asks which initial platform components to seed: `recommended` (default), `minimal`, or `none` for a bare cluster.

The generated `podplane.cluster.vmconfig.*.json` files pin the dependencies used by the VM cloud-init scripts. The Podplane Terraform provider loads those JSON files before it renders VM userdata. Manifests are selected using each configured VM pool's architecture, independently of the machine running the CLI. If a required manifest is not cached, this command fetches it automatically. To update an already cached manifest, run `podplane deps download --arch <architecture>`, then re-run this command to update the relevant pinned copy.

```
podplane cluster create [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `-f, --cluster-config string` | Path to the cluster config file (default: `podplane.cluster.jsonc` in the current directory) |
| `--no-apply` | Generate OpenTofu/Terraform files but do not run apply |
| `-y, --auto-approve` | Skip confirmation prompts and pass auto-approval to OpenTofu/Terraform |
