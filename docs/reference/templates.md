---
title: "Templates"
linkTitle: "Deployment Templates"
weight: 25
description: "App deployment templates for web apps and background workers"
aliases:
  - /docs/templates/
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

The `web` template deploys a web application with ingress routing and two separate TLS layers. Envoy Gateway terminates browser-facing ingress TLS, while the web Pod uses an operator-signed Service certificate for encrypted gateway-to-workload traffic.

**Component dependencies:** podplane-operator, secrets-store-csi-driver, envoy-gateway

The certificate projections require Kubernetes 1.37 or later, where Pod Certificates and ClusterTrustBundles are stable and enabled by default.

### What You Get

- A Deployment running your container alongside an Envoy sidecar for service TLS termination
- A ClusterIP Service exposing HTTPS (port 443)
- A Gateway API HTTPRoute attached to the platform Envoy Gateway
- An automatically rotating Kubernetes `podCertificate` for service TLS
- A BackendTLSPolicy ensuring encrypted gateway-to-service traffic
- An optional ServiceAccount SPIFFE identity for application-managed mTLS

Your app container serves plain HTTP on port 8080 by default — the Envoy sidecar handles service TLS. Set `certificates.server=direct` when the app should receive the serving certificate and terminate TLS itself. Use `--set app.port=<port>` if your image listens on a different primary port.

### Template values

Use [`podplane deploy`](cli/commands/deploy.md) flags for universal inputs such as app name, optional image override, and environment variables.

The web template also supports the ergonomic routing flags `--hostname` and `--path`. For non-standard external HTTPS ports, set `route.port` with `--set route.port=<port>`.

Template-specific values can be set with `--set` e.g.:

| Value | Default | Description |
|---|---|---|
| `images.app` | `ghcr.io/podplane/hello:latest` | App container image default; `--image` maps here |
| `images.envoy` | `docker.io/envoyproxy/envoy:distroless-v1.37-latest` | Envoy sidecar image |
| `app.env` | `{}` | Non-secret environment variables for the app container; `--env` maps here |
| `app.port` | `8080` | App port, or an array with the primary port first and additional Service ports after it |
| `route.hostname` | `""` | External hostname for routing; `--hostname` maps here |
| `route.path` | `/` | URL path prefix for routing; `--path` maps here |
| `route.port` | `443` | External HTTPS port for the browser-facing route URL |
| `certificates.server` | `sidecar` | Terminate service TLS in the Envoy `sidecar` or `direct` in the app |
| `certificates.client` | `false` | Project a ServiceAccount SPIFFE identity and workload trust bundle |

Kubernetes projects the serving key and certificate chain together at
`/var/run/secrets/podplane/server-certificate/credential-bundle.pem`. The
Podplane signer authorizes the chart's selecting Service and issues its four
cluster DNS names. Kubelet projects the certificate directly, without creating
a Kubernetes TLS Secret. In sidecar mode, Envoy's filesystem SDS watches the
projected directory and reloads valid rotations without restarting the Pod. In
direct mode the app reads the same bundle and must reload it after atomic
rotation.

When `certificates.client=true`, the app receives a separate SPIFFE credential
at `/var/run/secrets/podplane/client-certificate/credential-bundle.pem` and the
workload roots at
`/var/run/secrets/podplane/client-certificate/trust-bundle.pem`. The identity is
`spiffe://<trust-domain>/ns/<namespace>/sa/<service-account>`. Applications must
validate the chain and explicitly authorize peer identities; trusting the CA
alone is not authorization.

To expose additional cluster-internal ports, use quoted Helm list syntax. The first port remains the primary port; later ports target the app directly and cannot use the public Service port 443:

```bash
podplane deploy web --name hello --set 'app.port={8080,8082}'
```

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
    → Envoy Gateway
      → BackendTLSPolicy (verified backend TLS)
        → Service (:443)
          → Envoy sidecar (TLS termination, reverse proxy to localhost:<port>)
            → App container (<port>, plain HTTP)
```

The Envoy sidecar mounts the projected Service certificate and reverse proxies to your app on `127.0.0.1:<port>`. The BackendTLSPolicy verifies the Service hostname against the operator-published Podplane workload ClusterTrustBundle. This is server-authenticated TLS, not mTLS: Envoy Gateway does not present a client certificate to the workload.

## `worker`

The `worker` template deploys a background worker process with no ingress or server TLS.

**Component dependencies:** podplane-operator, Secrets Store CSI Driver

### What You Get

- A Deployment running your container
- No Service, ingress, or server TLS—the worker is not externally reachable
- An optional ServiceAccount SPIFFE identity for application-managed mTLS

This is suitable for queue consumers, cron-like processors, or any workload that initiates its own outbound connections rather than serving HTTP traffic.

### Template values

Use [`podplane deploy`](cli/commands/deploy.md) flags for universal inputs such as worker name, optional image override, and environment variables. Worker-specific configuration should be exposed as schema-backed template values and set with `--set`.

Set `certificates.client=true` to project the worker's SPIFFE credential and
workload trust bundle at
`/var/run/secrets/podplane/client-certificate/credential-bundle.pem` and
`/var/run/secrets/podplane/client-certificate/trust-bundle.pem`. The credential
is generated and rotated by kubelet, is never Secret-backed, and uses the
worker Pod's explicit ServiceAccount identity.

### Example

```bash
podplane deploy worker \
  --name email-sender \
  --image myorg/email-sender:latest \
  -e QUEUE=default
```

## Template values contract

Every template chart must include `values.schema.json`. The schema is the contract for supported template values and is used by Podplane to validate common ergonomic flags before invoking Helm.

`podplane deploy` keeps common flags for release name, image and environment overrides, simple secret bindings, routing shortcuts, Helm value overrides, namespace and Kubernetes context selection, readiness waiting, timeout, and approval. These apply to deploy itself rather than to any one template; see the [`deploy` reference](cli/commands/deploy.md) for the complete list.

Template charts must put container image values under `images`. The `--image` flag maps to the app workload image, conventionally `images.app`; template-owned support images use sibling keys such as `images.envoy`. This gives Podplane one predictable place to inspect, prefetch, mirror, or override image references.

Template manifests include a flat `templates.images` list, modelled after the components image manifest. Each row records a resolved image for one platform (`image`, `digest`, `size`, `platform`, and optional `index`) plus a `templates` map from template name to the image key under `images`. For example, `"templates": {"web": "envoy"}` means the image is referenced by the web template at `images.envoy`.

When the cached cluster summary enables a registry mirror, `podplane deploy` uses this manifest metadata to inject mirrored refs for template image defaults. Explicit user overrides are preserved: `--image` prevents generated mirror injection for `images.app`, and `--set images.<key>=...` prevents generated mirror injection for that image key.

Some flags are common ergonomic shortcuts for template values. Today `--hostname` maps to `route.hostname`, and `--path` maps to `route.path`. Because not every template supports routing, the deploy command checks the template's `values.schema.json` and fails loudly if one of these flags is used with an unsupported template.

Template-specific configuration uses Helm-compatible `--set` syntax instead of dedicated Podplane flags:

```bash
podplane deploy web --name hello --image ghcr.io/podplane/hello:latest \
  --set app.port=8080
```

Quote values containing Helm list syntax so the shell passes the braces unchanged:

```bash
podplane deploy web --name hello --set 'app.port={8080,8082}'
```
