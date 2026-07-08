---
title: "push"
---

# podplane push

Push an app image from Podplane's local registry to the current cluster's container image registry through a Kubernetes port-forward.

```sh
podplane push <local-image> [<remote-image>] [flags]
```

This is the promotion path for images built with `podplane build`: build and test locally, then push the same `apps/` image to another Podplane cluster.

For example:

```sh
# build your app image
podplane build -t api:v1
# deploy it in your local cluster to test it
podplane deploy web --name api --image default-registry.local/apps/api:v1
# now push it to your remote cluster
podplane push api:v1
# and deploy it in your remote cluster using that cluster's registry hostname
podplane deploy web --name api --image prod-registry.example.com/apps/api:v1
```

If `<remote-image>` is omitted, Podplane pushes to:

```text
<cluster.registry.hostname>/<source-app-repository>:<source-tag-or-latest>
```

Source images are normalized the same way as `podplane build` output. These are equivalent source refs:

```text
api:v1
apps/api:v1
default-registry.local/apps/api:v1
```

All resolve to `apps/api:v1` in Podplane's local registry cache.

If `<remote-image>` omits a registry hostname, Podplane prefixes `cluster.registry.hostname`. Remote images must be under `apps/**`; `mirror/**` is reserved for Podplane-managed dependency mirrors.

If the source image is not in Podplane's local registry cache, Podplane checks Docker's local image store. When a matching Docker image exists, Podplane asks before importing and pushing it. Use `--auto-approve` to skip the prompt, `--docker=/path/to/docker` to choose a Docker binary, or `--docker=false` to disable this fallback.

## Options

| Flag | Description |
| --- | --- |
| `--context string` | Kubeconfig context to use. Defaults to the current kubeconfig context. |
| `--kubeconfig string` | Path to the kubeconfig file. |
| `--docker[=PATH\|false]` | Use Docker as a fallback source, optionally with a Docker binary path. Defaults to `docker`; use `--docker=false` to disable fallback. |
| `-y, --auto-approve` | Skip confirmation prompts. |

The command requires the `zot-registry` component to be installed and ready in `platform-zot-registry` in the target cluster.
