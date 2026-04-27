---
title: "Templates"
weight: 60
description: "App deployment templates for web apps and background workers"
---

# Templates

App templates are opinionated Helm charts that make it easy to deploy common workload types via `podplane deploy`. Each template handles the boilerplate — networking, TLS, ingress - so you only need to provide the basics like an app name and your container image.

```bash
podplane deploy <template> --name <name> --image <image>
```

Templates may have component dependencies. If the required addon components aren't installed, the CLI will prompt you to install them.

## `web`

The `web` template deploys a web application with automatic TLS and ingress routing.

**Component dependencies:** traefik, cert-manager, gateway-api-crds

### What You Get

- A Deployment running your container alongside a Caddy sidecar for TLS termination
- A ClusterIP Service exposing HTTPS (port 443)
- A Gateway API HTTPRoute attached to the platform's Traefik gateway
- A cert-manager Certificate for pod-internal mTLS
- A BackendTLSPolicy ensuring encrypted gateway-to-service traffic

Your app container serves plain HTTP on port 80 — the Caddy sidecar handles all TLS. No TLS configuration is needed in your app.

### Options

| Option | Default | Description |
|---|---|---|
| `--name` | *(required)* | App name, used as prefix for all Kubernetes resources |
| `--image` | *(required)* | Container image for the application |
| `--hostname` | *(none)* | External hostname for routing (matches any hostname if omitted) |
| `--path` | `/` | URL path prefix for routing |

### Example

```bash
podplane deploy web --name myapp --image myorg/myapp:latest --hostname myapp.example.com
```

### How It Works

```
External Traffic
  → HTTPRoute (hostname + path matching)
    → Traefik Gateway
      → BackendTLSPolicy (verified mTLS)
        → Service (:443)
          → Caddy sidecar (TLS termination, reverse proxy to localhost:80)
            → App container (:80, plain HTTP)
```

The Caddy sidecar mounts a cert-manager-issued TLS certificate and reverse proxies to your app on `127.0.0.1:80`. The BackendTLSPolicy verifies the connection from the gateway to the service using the platform's self-signed CA bundle.

## `worker`

The `worker` template deploys a background worker process with no ingress or TLS.

**Component dependencies:** None

### What You Get

- A Deployment running your container
- No Service, no ingress, no TLS — the worker is not externally reachable

This is suitable for queue consumers, cron-like processors, or any workload that initiates its own outbound connections rather than serving HTTP traffic.

### Options

| Option | Default | Description |
|---|---|---|
| `--name` | *(required)* | App name, used as prefix for all Kubernetes resources |
| `--image` | *(required)* | Container image for the worker |

### Example

```bash
podplane deploy worker --name email-sender --image myorg/email-sender:latest
```
