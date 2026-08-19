---
title: "cluster upgrade"
weight: 15
description: "Upgrade cluster infrastructure dependencies"
---

## Overview

Downloads the latest compatible OpenTofu/Terraform modules and providers,
adds them to the shared dependency cache, and moves an existing cluster to
those versions. The command regenerates managed cluster files, updates the
cluster's `.terraform.lock.hcl`, initializes the stack from the shared provider
mirror, and asks before applying the upgrade.

```shell
podplane cluster upgrade [flags]
```

The cluster must already have been generated with `cluster create`. Ordinary
`cluster create` runs remain reproducible: they restore the versions recorded
by the generated module paths and provider lock instead of upgrading them. Use
`cluster upgrade` only when intentionally moving the cluster to newer
dependency versions.

## Options

| Flag | Description |
| --- | --- |
| `-f, --cluster-config string` | Path to the cluster config file (default: `podplane.cluster.jsonc` in the current directory) |
| `--no-apply` | Upgrade generated files and the provider lock but do not run apply |
| `-y, --auto-approve` | Skip confirmation and pass auto-approval to OpenTofu/Terraform |
