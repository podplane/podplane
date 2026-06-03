---
title: "local server"
weight: 77
description: "Run a local background server for VMs"
---

## Overview

Runs a local background server that VMs use to access required files and services:

1. The `podplane deps download` dependency cache, containing `vmconfig` packages and `components` container images
2. A fake OIDC server, enabling `podplane login` to automatically authenticate with local cluster
3. A fake S3 server for local clusters to use for bucket/object storage.

This `local server` command is run automatically in the background when `podplane local start` is used, and stopped on `podplane local stop` of the last running VM. There is only one shared local server process for all local clusters.

The server also terminates host-facing local ingress TLS on `https://<host>.<cluster-id>.localhost:4433`, dynamically selecting or generating a [mkcert](https://mkcert.dev/)-issued certificate for `<cluster-id>.localhost` and `*.<cluster-id>.localhost` based on the requested hostname, then reverse-proxies to Traefik inside the VM. This requires mkcert to be installed prior to running a local VM, and the `local server` command will run `mkcert -install` before starting a local cluster if it has not yet been run, so that you get browser-trusted local ingress certificates.

The fake S3 service exposes durable local-cluster buckets such as `<cluster-id>-netsy` and `<cluster-id>-telemetry` under `/s3/data/`. Cache-backed buckets are exposed separately under `/s3/cache/`; currently this includes the shared `registry` bucket used by zot for mirrored component images from the `components/images` directory under the deps cache.

```
podplane local server [flags]
```

## Options

| Flag | Description |
| --- | --- |
| `-a, --addr string` | Address to bind server to (default: `0.0.0.0`) |
| `-b, --background` | Run the server in the background; set when `podplane local start` invokes this command |
| `-q, --stop` | Stop the existing server process instead of starting one |
| `--id string` | Local cluster ID (default: `default`) |
