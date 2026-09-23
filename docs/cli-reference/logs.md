---
title: "logs"
weight: 52
description: "Tail logs for a deployed app"
---

## Overview

Tails logs for a deployed app. Under the hood this wraps `kubectl logs`.

```
podplane logs <name> [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `-c, --container string` | Container to tail. When omitted in an interactive terminal, Podplane selects the default or prompts if needed. |
| `--all` | Tail all containers with prefixed lines. Cannot be combined with `--container`. |
| `-n, --namespace string` | Kubernetes namespace the app was deployed into |
| `--context string` | The name of the kubeconfig context to use (default: current kubeconfig context) |
| `--kubeconfig string` | Path to the kubeconfig file (default: `$KUBECONFIG` or `~/.kube/config`) |
