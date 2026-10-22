---
title: "local start"
weight: 70
description: "Start a local cluster VM"
aliases:
  - /docs/cli-reference/local-start/
---

## Overview

Starts a local single-node cluster VM, creating it if it doesn't exist. Packages are automatically downloaded and cached if not already present. A local server (serving packages and hosting a fake OIDC server) is started in the background.

Use `--console` to attach to the VM serial console after startup completes. This behaves like running [`podplane local console`](local-console.md) immediately after `local start`; press `Ctrl-]` to detach without stopping the VM.

Use `--follow` to stream `/var/log/cloud-init-output.log` while `local start` waits for cloud-init user-data to complete.

Use `--id` to select a non-default local cluster.

Persistent volumes use a separate sparse qcow2 data disk, initially `256G`;
the root disk remains `128G`. Use `--data-disk-size 512G` or `--data-disk-size 1T`
to choose or increase its capacity. The flag accepts `qemu-img` size syntax.
When omitted, an existing disk is never resized. Growth requires a stopped VM;
the next boot grows its LVM physical volume before kubelet starts. Shrinking is
unsupported, and a running VM accepts the flag only when it equals the current
size. A missing disk is created on every start.

Use `--components` to select which platform components are seeded into Netsy when creating a local cluster. `minimal`, `platform`, and `agentic` (default) use their corresponding `.netsy` Podplane seed files. Use the `platform` seed to provision a traditional PaaS, and the `agentic` seed to provision an Agentic PaaS. `none` skips seeding entirely; you get a bare cluster, whose Pods run under runc without Podplane's runtime policies, and are responsible for bootstrapping platform components yourself (for example, by running `DOMAIN=default.localhost make platform` or `make agentic` from the components repo against the default local cluster). For seeded clusters, the selected seed name, version, and digest are recorded in the auto-generated `cluster.jsonc` under `cluster.seed`; for `none`, `cluster.seed` is written as an empty object.

Seeding is only performed during cluster creation / first boot. Later runs of `podplane local start` never overwrite existing Netsy state, and explicitly passing `--components` for an existing local cluster returns an error. To use a different initial component set, delete the local cluster and create it again with the desired `--components` value.

```
podplane local start [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `--id string` | Local cluster ID (default: `default`) |
| `-c, --cpus string` | CPUs to allocate to the VM (default `2`) |
| `-m, --memory string` | Memory to allocate to the VM (default `4G`) |
| `--data-disk-size string` | Persistent data disk capacity (default `256G` for a missing disk); explicitly set to grow a stopped VM's disk |
| `--console` | Attach to the VM serial console after startup |
| `--follow` | Stream cloud-init user-data logs while waiting for startup |
| `--components string` | Platform components seeded when creating a local cluster: `none`, `minimal`, `platform`, or `agentic` (default). This flag is rejected when starting existing local clusters. |
