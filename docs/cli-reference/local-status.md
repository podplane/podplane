---
title: "local status"
weight: 71
description: "Report the status of a local cluster VM"
---

## Overview

Reports the status of a local cluster VM.

Use `--id` to select a non-default local cluster.

Use `--json` when scripts need machine-readable local runtime details, such as
the local VM node IP, the local HTTPS forwarder port, and the effective
components Git source used by local development workflows.

```
podplane local status [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `--id string` | Local cluster ID (default: `default`) |
| `--json` | Print local status as JSON |

## JSON output

`--json` includes the local server and components source details that developer
automation can consume without hardcoding local runtime values:

The `components.source.secretRef.name` field is used by local development
bootstrap to wire Flux GitRepository to the local HTTPS server CA Secret.

```sh
podplane local status --json
```

Example fields:

```json
{
  "cluster_id": "default",
  "running": true,
  "vm": {
    "provider": "qemu",
    "node_ip": "10.0.2.15",
    "forward_https_port": 19443
  },
  "local_server": {
    "running": true,
    "http_port": 12345,
    "https_port": 23456,
    "ca_cert_file": "/Users/example/.podplane/local-server/tls/oidc/ca.pem"
  },
  "components": {
    "source": {
      "url": "https://10.0.2.15:19443/git/components.git",
      "ref": {
        "branch": "local-dev"
      },
      "secretRef": {
        "name": "podplane-components-git"
      }
    }
  }
}
```
