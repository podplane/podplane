---
title: "local sync"
weight: 76
description: "Sync files into a local cluster VM"
---

## Overview

Rsyncs files into the local cluster VM. This command exists primarily for Podplane development work on the `vmconfig` package.

Use `--id` to select a non-default local cluster. Exactly one source path and one destination path are required.

```
podplane local sync [from] [to] [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `--chown string` | Rsync chown flag |
| `--exclude stringArray` | Rsync exclude pattern; may be specified multiple times |
| `--id string` | Local cluster ID (default: `default`) |
