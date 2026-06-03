---
title: "local console"
weight: 75
description: "Attach to a local cluster VM serial console"
---

## Overview

Attaches your terminal to the local cluster VM's serial console. This is useful for boot and login debugging, especially before SSH access has been configured by user-data.

For local direct-boot VMs, Podplane enables a dev-only systemd debug shell on the serial console, so this can still provide a root shell when cloud-init user-data fails.

Press `Ctrl-]` to detach from the console without stopping the VM.

Use `--id` to select a non-default local cluster.

```
podplane local console [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `--id string` | Local cluster ID (default: `default`) |
