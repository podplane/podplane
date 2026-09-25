---
title: "cluster delete"
weight: 11
description: "Remove deployed cluster infrastructure"
aliases:
  - /docs/cli-reference/cluster-delete/
---

## Overview

Removes deployed infrastructure. For AWS and Google Cloud clusters, Podplane first scales Nstance-managed groups to zero in the shard configs, waits for managed VM instances to terminate, falls back to confirmed direct VM termination if managed instances remain, runs OpenTofu/Terraform destroy, then offers to terminate any remaining VM instances tagged/labelled for the cluster.

The cluster config and generated `.tf` files are left in place.

```
podplane cluster delete [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `-f, --cluster-config string` | Path to the cluster config file (default: `podplane.cluster.jsonc` in the current directory) |
| `--no-apply` | Validate the config but do not run destroy |
| `-y, --auto-approve` | Skip confirmation prompts and pass auto-approval to OpenTofu/Terraform |
