---
title: "logout"
weight: 31
description: "Remove cluster authentication from kubeconfig"
aliases:
  - /docs/cli-reference/logout/
---

## Overview

Removes the previously authenticated cluster from your kubeconfig via kubectl.
By default, Podplane resolves the cluster from the current kubeconfig context.
Use `--cluster` or `-f, --cluster-config` when the kubeconfig context is missing
or you want to clean up a specific cluster.

```
podplane logout [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `-y, --auto-approve` | Skip confirmation prompts |
| `--cluster string` | Cluster ID |
| `-f, --cluster-config string` | Path to a podplane.cluster.jsonc file |
| `--context string` | The name of the kubeconfig context to use |
| `--kubeconfig string` | Path to the kubeconfig file |
