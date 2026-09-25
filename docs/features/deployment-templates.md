---
title: "Single-command deployments with Helm-based app templates"
linkTitle: "Deployment Templates"
weight: 50
description: "Deploy common workload types without writing the Kubernetes manifests or learning Helm"
---

Podplane templates package a working set of Kubernetes resources behind one approachable command:

```bash
podplane deploy web \
  --name hello \
  --image ghcr.io/example/hello:v1 \
  --hostname hello.example.com
```

The built-in `web` template creates the Deployment, Service, Gateway API route, and workload TLS configuration for an HTTP application. By default, it will sidecar envoy to handle TLS termination for you, but this can easily be disabled if your application can handle TLS termination itself.

There is also a `worker` template which provides a simpler Deployment for background processes with no public route.

## Opinionated, but still Helm

Podplane templates are Helm charts with a JSON Schema contract. Common settings such as the application name, image, environment variables, secret bindings, and route have friendly CLI flags. Less common chart values remain available through `--set`.

You can also use any Kubernetes manifest or tool such as Helm yourself - Podplane runs the standard Kubernetes kube-apiserver and controller-manager/scheduler.

When using the `podplane deploy` command, it checks component dependencies before deploying and can offer to install missing addons such as Envoy Gateway (for example, if you used the `minimal` seed and haven't got Envoy Gateway pre-installed).

Under the hood, `podplane deploy` uses `helm upgrade --install`, waits for resources to become ready by default, and preserves normal Helm release behavior.

Templates also understand Podplane's registry mirror and workload certificate conventions. Explicit image overrides remain explicit; Podplane does not replace an image chosen by the user.

## Learn more

- [Templates guide](../templates.md) — values, resources, TLS behavior, and image conventions.
- [`podplane deploy` reference](../reference/cli/commands/deploy.md)
- [Helm charts](https://helm.sh/docs/topics/charts/)
