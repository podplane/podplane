---
title: "hooks kubectl-auth"
weight: 40
description: "kubectl exec auth plugin"
---

## Overview

Used as a kubectl exec auth plugin for cluster authentication. This command is typically not invoked directly but is configured as a credential plugin in your kubeconfig.

```
podplane hooks kubectl-auth [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `-c, --cluster string` | Cluster ID (required) |
| `-u, --user string` | User sub (required) |
