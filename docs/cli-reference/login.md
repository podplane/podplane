---
title: "login"
weight: 30
description: "Authenticate to a cluster via kubectl"
---

## Overview

Authenticates to a cluster via kubectl using the auth URL specified in the cluster configuration file. The authentication credentials are stored in your kubeconfig.

```
podplane login [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `-f, --cluster-config string` | Path to a podplane.cluster.jsonc file (default: `./podplane.cluster.jsonc`) |
| `--ca-cert string` | Path, URL, or inline PEM for the Kubernetes API server CA certificate |
| `--callback-port int` | Port for the local OIDC callback HTTP server (default: `8000`) |
| `--headless` | Skip opening a browser; follow the authorize redirect non-interactively |
