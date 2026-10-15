---
title: "cluster sleep"
weight: 17
description: "Request cluster sleep to reduce idle compute costs"
---

## Overview

Requests cluster sleep to reduce idle compute costs. The command returns once
the request is submitted, without waiting for the cluster to sleep. Normal sleep
proceeds only if the cluster passes its workload and inactivity checks.

Use `--force` to bypass those checks. **Forced sleep can interrupt active
workloads and connections.** It asks for confirmation; noninteractive use
requires `--auto-approve` (`-y`).

The target cluster comes from the current kubeconfig context. Use `--context` to
select a different cluster or `--kubeconfig` to select a different kubeconfig file.

Traffic to a Kubernetes API or ingress endpoint configured for wake brings the
cluster back online. Scheduled wake timers can also wake it; there is no separate wake
command. Wake takes time to boot the nodes and restore service.

Sleep is supported on AWS and Google Cloud clusters using the `recommended`
seed. See [sleep and wake behavior](../../configuration.md#exposure-nat-and-sleep).

```shell
podplane cluster sleep [flags]

# Request sleep for a specific kubectl context
podplane cluster sleep --context production

# Force sleep, with an interactive confirmation
podplane cluster sleep --force
```

## Options

| Flag | Description |
| --- | --- |
| `--context string` | Kubeconfig context (default: current context) |
| `--kubeconfig string` | Kubeconfig file; otherwise uses kubectl's normal selection |
| `--force` | Bypass guards after an interruption warning and explicit approval |
| `-y, --auto-approve` | Explicitly approve forced sleep without prompting |
