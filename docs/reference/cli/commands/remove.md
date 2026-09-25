---
title: "remove"
weight: 51
description: "Remove a previously deployed app"
aliases:
  - /docs/cli-reference/remove/
---

## Overview

Removes a previously deployed app from the cluster.

This is a convenience command which wraps `helm` commands.

```
podplane remove --name <name> [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `--name string` | Name of the app deployment to remove (required) |
| `-n, --namespace string` | Kubernetes namespace the app was deployed into |
| `--context string` | The name of the kubeconfig context to use |
| `--kubeconfig string` | Path to the kubeconfig file (default: `$KUBECONFIG` or `~/.kube/config`) |
