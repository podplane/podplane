---
title: "local shell"
weight: 74
description: "Open a shell into a local cluster VM"
---

## Overview

Opens a shell into the local cluster VM or runs a command via SSH. This command exists primarily for Podplane development work on the `vmconfig` package.

This requires SSH access inside the guest. For boot debugging or before SSH is configured, use [`podplane local console`](local-console.md).

Use `--id` to select a non-default local cluster. Pass an optional command argument to run that command over SSH instead of opening an interactive shell.

```
podplane local shell [command] [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `--id string` | Local cluster ID (default: `default`) |
