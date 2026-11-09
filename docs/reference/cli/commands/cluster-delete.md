---
title: "cluster delete"
weight: 11
description: "Remove deployed cluster infrastructure"
aliases:
  - /docs/cli-reference/cluster-delete/
---

## Overview

Starts by allowing you to choose what to remove using a list of checkboxes 
covering: infrastructure, cluster buckets (names shown together), the
Terraform-state bucket, generated cluster Terraform and support files, and the
cluster config. All checkboxes are checked by default. Use ↑/↓ and Space to select
actions, then Tab to **Delete selected** and Enter to confirm. Unchecking an action
clears dependent actions; they cannot be selected until their prerequisites are
checked.

Custom managed resources in the same Terraform root are included in deletion.
Podplane lists them and asks for confirmation before teardown; `--auto-approve`
prints the warning and accepts it. Custom `.tf` files alone do not trigger a warning.

Podplane drains Nstance-managed VMs and destroys infrastructure while retaining
storage. After Nstance servers and workload VMs have terminated, it empties
selected cluster buckets—including old object versions—and lets Terraform
delete them. Retained storage and its dependencies stay tracked in state.

The state bucket is deleted only after no managed resources remain. Local files
are removed last; failures preserve them for recovery. User-owned Terraform files
are left alone. Removing generated files clears the stack's `.podplane/` except
`cluster-schema.json`, which is removed with the cluster config. The same cleanup
action removes `.terraform/` and `.terraform.lock.hcl`; other stack directories
and custom `.tf` source files are left alone.
**Bucket deletion is permanent.**

Podplane holds the OpenTofu/Terraform backend lock throughout teardown. For state
bucket cleanup, it removes the lock last, then attempts bucket deletion. If another
client writes to the bucket first, deletion fails without emptying it again.
Do not bypass or force-unlock an active deletion. Cluster deletion requires the
default Terraform workspace and a dedicated state bucket.

```
podplane cluster delete [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `-f, --cluster-config string` | Path to the cluster config file (default: `podplane.cluster.jsonc` in the current directory) |
| `--no-apply` | Validate the config but do not run destroy |
| `-y, --auto-approve` | Approve **all** actions, including bucket data, state, generated `.tf` files, and cluster config |
