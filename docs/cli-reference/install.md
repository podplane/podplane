---
title: "install"
weight: 60
description: "Install an addon component into the cluster"
---

## Overview

Installs an addon component into the cluster with an opinionated, tested
configuration. Components are installed and managed by Flux CD.

The CLI patches the `platform-components` HelmRelease so Flux
CD reconciles the component (and any of its dependencies that are not yet
enabled).

After patching, the CLI displays reconciliation status and waits for the
component and enabled dependencies to become Ready.

If dependencies need to be enabled, you are prompted for confirmation before
the patch is applied. Pass `-y` / `--auto-approve` to skip the prompt.

You must already be authenticated to the cluster - run `podplane login` first
if you have not yet logged in.

The cluster state is the source of truth for installed addon components.

```
podplane install <component> [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `-f, --cluster-config string` | Path to the cluster config file (default: `podplane.cluster.jsonc`; required when enabling `podplane-operator`) |
| `--context string` | kubeconfig context to use (default: current kubeconfig context) |
| `--kubeconfig string` | Path to the kubeconfig file |
| `-y, --auto-approve` | Skip confirmation prompts |
