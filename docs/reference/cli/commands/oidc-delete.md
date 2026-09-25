---
title: "oidc delete"
weight: 21
description: "Remove deployed OIDC infrastructure"
aliases:
  - /docs/cli-reference/oidc-delete/
---

## Overview

Removes deployed infrastructure. The OIDC config and generated `.tf` files are left in place.

```
podplane oidc delete [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `-f, --oidc-config string` | Path to the OIDC config file (default: `podplane.oidc.jsonc` in the current directory) |
| `--no-apply` | Validate the config but do not run destroy |
| `-y, --auto-approve` | Skip confirmation prompts and pass auto-approval to OpenTofu/Terraform |
