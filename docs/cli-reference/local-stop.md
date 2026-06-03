---
title: "local stop"
weight: 72
description: "Stop a local cluster VM"
---

## Overview

Stops a local cluster VM. If this is the last running VM, the background local server is also stopped.

Use `--id` to select a non-default local cluster. Pass `--rm` to delete the cluster after stopping it.

```
podplane local stop [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `--rm` | Remove (delete) the cluster after stopping |
| `--id string` | Local cluster ID (default: `default`) |
