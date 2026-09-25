---
title: "local server"
weight: 77
description: "Run a local background server for VMs"
aliases:
  - /docs/cli-reference/local-server/
---

## Overview

Runs a local background server that VMs use to access required files and services:

1. The `podplane deps download` dependency cache and smart Git access to cached component repositories
2. NoCloud cloud-init data
3. A fake OIDC server, enabling `podplane login` to authenticate automatically with a local cluster
4. Fake S3 for durable local-cluster data and cache-backed registry storage
5. A fake Vault/OpenBao API for local Secrets Store CSI usage
6. Fake Nstance registration and agent gRPC services

This `local server` command is run automatically in the background when `podplane local start` is used. There is only one shared local server process for all local clusters. The current `podplane local stop` implementation stops that process whenever it stops a VM; it does not yet check whether another local VM is still running.

The server also terminates host-facing local ingress TLS on `https://<host>.<cluster-id>.localhost:4433`, dynamically selecting or generating a [mkcert](https://mkcert.dev/)-issued certificate for `<cluster-id>.localhost` and `*.<cluster-id>.localhost` based on the requested hostname, then reverse-proxies to the ingress gateway inside the VM. This requires mkcert to be installed prior to running a local VM. For your browser to trust the local ingress server certificates, please ensure you've run `mkcert -install` once.

The fake S3 service exposes durable local-cluster buckets such as `<cluster-id>-netsy` and `<cluster-id>-telemetry` under `/s3/data/`. Cache-backed buckets are exposed separately under `/s3/cache/`; currently this includes the shared `registry` bucket used by the node-local registry for mirrored images from the local registry cache.

The local server is the only process that opens the encrypted fake Vault store or its keyring encryption keys. Services inside local VMs use the HTTPS Vault-compatible API with Kubernetes service account authentication. Host-side lifecycle operations, such as creating the workload CA key and deleting/cleaning up a cluster's secrets, use the same API over a user-only Unix socket.

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
