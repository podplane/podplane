---
title: "uninstall"
weight: 61
description: "Remove an addon component from the cluster"
aliases:
  - /docs/cli-reference/uninstall/
---

## Overview

Removes a previously installed addon component from the cluster.

The CLI
patches the `platform-components` HelmRelease so Flux CD removes the
component's `HelmRelease`.

Please note:

- Core components cannot be uninstalled.
- CRD charts are never removed by `podplane uninstall` to avoid
  deleting custom resource data. To disable a CRD chart manually, set its
  `enabled` field to `false` in the `platform-components` HelmRelease
  values manually.
- A component cannot be uninstalled while other enabled components depend
  on it; uninstall the dependents first.

You must already be authenticated to the cluster - run `podplane login` first
if you have not yet logged in.

The cluster state is the source of truth for installed addon components.

```
podplane uninstall <component> [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `--context string` | kubeconfig context to use (default: current kubeconfig context) |
| `--kubeconfig string` | Path to the kubeconfig file |
| `-y, --auto-approve` | Skip confirmation prompts |
