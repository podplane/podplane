---
title: "local stop"
weight: 72
description: "Stop a local cluster VM"
aliases:
  - /docs/cli-reference/local-stop/
---

## Overview

Stops a local cluster VM and the shared background local server. The current implementation does not yet preserve the server when another local VM is running.

Use `--id` to select a non-default local cluster. Pass `--rm` to delete the cluster after stopping it.

Stopping preserves the root and persistent-volume data disks. `--rm` deletes
both disks, including every local persistent volume; this cannot be undone.

```
podplane local stop [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `--rm` | Remove (delete) the cluster after stopping |
| `--id string` | Local cluster ID (default: `default`) |
