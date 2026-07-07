---
title: "Templates"
weight: 60
description: "App deployment templates for web apps and background workers"
---

# Templates

App templates are opinionated Helm charts that make it easy to deploy common workload types via `podplane deploy`. Each template handles the boilerplate - networking, TLS, ingress - so you only need to provide the basics like an app name and, when you do not want the template default, your container image.

```bash
podplane deploy <template> --name <name> [--image <image>]
```

Environment variables can be set with Docker-style `-e` / `--env` flags:

```bash
podplane deploy web --name hello --image ghcr.io/podplane/hello:latest \
  -e HELLO_MESSAGE="G'Day World!"
```

Use `KEY=value` to pass an explicit value, or `KEY` to read the value from the local environment. Environment variables are stored in the rendered Deployment and Helm release metadata, so use them for non-secret configuration only.

Templates may have component dependencies. If the required addon components aren't installed, the CLI will prompt you to install them.

To update an existing app (e.g. to deploy a new image version), simply re-run `podplane deploy` with the same `--name`.

Deploy requires a cached cluster summary for the selected kubeconfig context. `podplane login -f <podplane.cluster.jsonc>` writes this summary for remote clusters, and `podplane local start` writes it for local clusters. This summary is used to determine if a registry mirror should be interpolated into template default image references.

Under the hood deploy runs `helm upgrade --install --wait --timeout 2m` by default, so Helm waits for rendered resources to become ready before printing chart notes. Use `--wait=false` to skip readiness waiting or `--timeout` to allow more time.

## `web`

The `web` template deploys a web application with automatic TLS and ingress routing.

**Component dependencies:** cert-manager, traefik, platform-trust

### What You Get

- A Deployment running your container alongside a Caddy sidecar for TLS termination
- A ClusterIP Service exposing HTTPS (port 443)
- A Gateway API HTTPRoute attached to the platform's Traefik gateway
- A cert-manager Certificate for pod-internal mTLS
- A BackendTLSPolicy ensuring encrypted gateway-to-service traffic

Your app container serves plain HTTP on port 8080 by default - the Caddy sidecar handles all TLS. No TLS configuration is needed in your app. Use `--set app.port=<port>` if your image listens on a different plain HTTP port.

### Template values

Use [`podplane deploy`](./cli-reference/deploy.md) flags for universal inputs such as app name, optional image override, and environment variables.

The web template also supports the ergonomic routing flags `--hostname` and `--path`. For non-standard external HTTPS ports, set `route.port` with `--set route.port=<port>`.

Template-specific values can be set with `--set` e.g.:

| Value | Default | Description |
|---|---|---|
| `images.app` | `ghcr.io/podplane/hello:latest` | App container image default; `--image` maps here |
| `images.caddy` | `docker.io/library/caddy:2` | Caddy sidecar image |
| `app.env` | `{}` | Non-secret environment variables for the app container; `--env` maps here |
| `app.port` | `8080` | Plain HTTP port exposed by the app container |
| `route.hostname` | `""` | External hostname for routing; `--hostname` maps here |
| `route.path` | `/` | URL path prefix for routing; `--path` maps here |
| `route.port` | `443` | External HTTPS port for the browser-facing route URL |
| `metrics.http` | `true` | Enable Caddy HTTP metrics |

### Example

```bash
podplane deploy web \
  --name hello \
  --image ghcr.io/podplane/hello:latest \
  --hostname hello.example.com \
  -e HELLO_MESSAGE="G'Day World!"
```

### How It Works

```
External Traffic
  → HTTPRoute (hostname + path matching)
    → Traefik Gateway
      → BackendTLSPolicy (verified mTLS)
        → Service (:443)
          → Caddy sidecar (TLS termination, reverse proxy to localhost:<port>)
            → App container (<port>, plain HTTP)
```

The Caddy sidecar mounts a cert-manager-issued TLS certificate and reverse proxies to your app on `127.0.0.1:<port>`. The BackendTLSPolicy verifies the connection from the gateway to the service using the platform's self-signed CA bundle.

## `worker`

The `worker` template deploys a background worker process with no ingress or TLS.

**Component dependencies:** None

### What You Get

- A Deployment running your container
- No Service, no ingress, no TLS - the worker is not externally reachable

This is suitable for queue consumers, cron-like processors, or any workload that initiates its own outbound connections rather than serving HTTP traffic.

### Template values

Use [`podplane deploy`](./cli-reference/deploy.md) flags for universal inputs such as worker name, optional image override, and environment variables. Worker-specific configuration should be exposed as schema-backed template values and set with `--set`.

### Example

```bash
podplane deploy worker \
  --name email-sender \
  --image myorg/email-sender:latest \
  -e QUEUE=default
```

## Template values contract

Every template chart must include `values.schema.json`. The schema is the contract for supported template values and is used by Podplane to validate common ergonomic flags before invoking Helm.

`podplane deploy` keeps a small stable set of universal flags: `--name`, `--image`, `-e` / `--env`, `--namespace`, Kubernetes context flags, and `--auto-approve`. These apply to deploy itself rather than to any one template.

Template charts must put container image values under `images`. The `--image` flag maps to the app workload image, conventionally `images.app`; template-owned support images use sibling keys such as `images.caddy`. This gives Podplane one predictable place to inspect, prefetch, mirror, or override image references.

Template manifests include a flat `templates.images` list, modelled after the components image manifest. Each row records a resolved image for one platform (`image`, `digest`, `size`, `platform`, and optional `index`) plus a `templates` map from template name to the image key under `images`. For example, `"templates": {"web": "caddy"}` means the image is referenced by the web template at `images.caddy`.

When the cached cluster summary enables a registry mirror, `podplane deploy` uses this manifest metadata to inject mirrored refs for template image defaults. Explicit user overrides are preserved: `--image` prevents generated mirror injection for `images.app`, and `--set images.<key>=...` prevents generated mirror injection for that image key.

Some flags are common ergonomic shortcuts for template values. Today `--hostname` maps to `route.hostname`, and `--path` maps to `route.path`. Because not every template supports routing, the deploy command checks the template's `values.schema.json` and fails loudly if one of these flags is used with an unsupported template.

Template-specific configuration uses Helm-compatible `--set` syntax instead of dedicated Podplane flags:

```bash
podplane deploy web --name hello --image ghcr.io/podplane/hello:latest \
  --set app.port=8080
```
