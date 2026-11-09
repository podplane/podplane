---
title: "oidc delete"
weight: 21
description: "Remove deployed OIDC infrastructure"
aliases:
  - /docs/cli-reference/oidc-delete/
---

## Overview

Uses the [TUI or Inline deletion workflow](../terminal-ui.md#deletion-workflow)
to prepare, confirm, and track destruction.

Removes deployed infrastructure, reading OpenTofu/Terraform state from the remote object storage bucket configured by `podplane oidc create`. The OIDC config, generated `.tf` files, and the state bucket are left in place.

Before initializing OpenTofu/Terraform, the command verifies the bucket's account/project, security settings, and OIDC hostname ownership markers. Missing or mismatched markers stop deletion, even with `--auto-approve`; see [OIDC state ownership](../oidc-create/). `--no-apply` validates the config without contacting the cloud.

```
podplane oidc delete [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `-f, --oidc-config string` | Path to the OIDC config file (default: `podplane.oidc.jsonc` in the current directory) |
| `--no-apply` | Validate the config but do not run destroy |
| `-y, --auto-approve` | Skip confirmation prompts and pass auto-approval to OpenTofu/Terraform |
