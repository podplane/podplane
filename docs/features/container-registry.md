---
title: "Built-in container registry on object storage"
linkTitle: "Container Registry"
weight: 10
description: "Store application and mirrored container images in OCI-compatible object storage via the built-in container registry"
---

Podplane gives each cluster its own OCI container registry. You can still connect and bring your own additional containe registries.

## Using the built-in container registry

Image data lives in object storage, so it survives registry Pod and VM replacement without requiring a dedicated persistent disk.

Use `podplane build` and `podplane push` to move an application from your workstation to the cluster registry:

```bash
podplane build -t api:v1 .
podplane push api:v1
```

Application images use the `apps/` namespace. Podplane-managed copies of platform dependencies use `mirror/`, keeping your images separate from the dependency mirror.

## How it works

The "writable" registry runs on your cluster as a Deployment and is powered by [Zot](https://zotregistry.dev/).

It uses OIDC authentication and role-based repository policies.

`podplane push` connects through a temporary Kubernetes port-forward, so the registry does not need to be exposed publicly. An optional Docker-compatible ingress is available when external registry access is required.

On each Kubernetes node, Podplane also runs a read-only registry service backed by the same object store - this mitigates bootstrap issues where Zot Registry would be unable to start due to its own container image being stored in the registry.

Components and deployment templates can render explicit mirrored image references, making it clear from a Kubernetes manifest when an image comes from the built-in cluster registry - Podplane does not silently intercept arbitrary image pulls as a transparent cache.

## Learn more

- [`podplane build`](../reference/cli/commands/build.md) and [`podplane push`](../reference/cli/commands/push.md)
- [Registry configuration](../reference/configuration.md)
- [Image mirroring for disconnected clusters](air-gapped-clusters.md#image-mirror)
