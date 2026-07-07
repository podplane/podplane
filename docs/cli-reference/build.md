---
title: "build"
description: "Build a container image for your local Podplane VM"
---

# podplane build

Builds an OCI container image from a `Containerfile` or `Dockerfile` using the [ocimage](https://github.com/podplane/ocimage) library and stores it in Podplane's local registry cache. Images built this way are immediately available to any local Podplane cluster/VM.

```sh
podplane build [PATH] [flags]
```

If no tag is provided, Podplane detects an app name from project metadata such as `package.json`, `go.mod`, `Cargo.toml`, or a single `.csproj`, then tags the image as:

```text
default-registry.local/apps/<app-name>:latest
```

Explicit tags are normalized into the local registry's `apps/` namespace. For example, `-t api`, `-t api:v1`, and `-t apps/api:v1` are stored as `apps/api` and printed as `default-registry.local/apps/api:<tag>`. Tags with a local registry host, such as `default-registry.local/apps/api:v1` or `other-registry.local/apps/api:v1`, are accepted and stored in the same shared registry cache path, `apps/api`.

`podplane build` rejects tags outside `apps/`, remote registry tags such as `ghcr.io/me/api:v1`, and `mirror/` tags. The `mirror/` namespace is reserved for Podplane-managed dependency mirrors.

After a successful build, Podplane prints a `podplane deploy web ... --image <built-image>` example and a `podplane logs <name>` command.

## Containerfile and Dockerfile detection

If `--file` is not set, ocimage selects the default build file. When neither `Containerfile` nor `Dockerfile` exists, Podplane tries to generate a conservative `Containerfile` for self-contained project templates such as Go, Rust, C# ASP.NET, and supported TypeScript examples - see [ocimage examples](https://github.com/podplane/ocimage/tree/main/examples) for the reference Containerfile examples.

Note: `Containerfile` is the OCI-neutral name for what many tools historically called a `Dockerfile`. You can still use `Dockerfile` if you prefer.

If multiple supported templates match, Podplane prompts you to choose one. If no safe template matches, Podplane prints an error and asks you to create a `Containerfile` or `Dockerfile` first.

## ocimage and Docker fallback

Podplane first tries to build with its built-in ocimage support directly. If your `Containerfile` or `Dockerfile` uses syntax ocimage cannot build, such as `RUN` or multi-stage `COPY --from`, Podplane falls back to Docker Buildx when available.

The fallback runs one `docker buildx build --output type=docker,dest=...` build per target platform, then imports the resulting Docker archive into Podplane's local registry cache under the same normalized `apps/` tag. This approach enables support for multi-arch image builds on par with the ocimage approach and avoids leaving temporary images in Docker's local image store.

If Docker or Buildx is not available, Podplane prints the unsupported instruction with file and line context and asks you to either simplify the build file or install Docker with Buildx support.

## Options

| Flag | Description |
| --- | --- |
| `-f, --file string` | Name of the Containerfile or Dockerfile |
| `-t, --tag stringArray` | Name and optionally a tag. May be specified multiple times. |
| `--platform string` | Target platform(s), comma-separated. Defaults to the local Podplane VM architecture. |
| `--build-arg KEY=VALUE` | Set build-time variables. May be specified multiple times. |
| `--label KEY=VALUE` | Set image labels. May be specified multiple times. |
| `--docker[=PATH\|false]` | Use Docker Buildx fallback, optionally with a Docker binary path. Defaults to `docker`; use `--docker=false` to disable fallback. |
| `--pull` | Always attempt to pull base images instead of using the local OCI store first |
| `--sbom[=true\|false]` | Generate an SBOM attestation with syft |

Docker-compatible flags that ocimage does not support, such as `--target`, `--no-cache`, `--output`, and `--provenance`, fail with a friendly error.

## Examples

Build the current directory and use Podplane's default local image name:

```sh
podplane build
```

Build with an explicit tag:

```sh
podplane build -t api:v1 .
```

Build with an explicit file and build argument:

```sh
podplane build -f Containerfile --build-arg VERSION=1.2.3 .
```

## Equivalent ocimage flow inside a cluster

`podplane build` is optimized for local VM development: it writes directly into the local registry cache and prints a deploy command.

If you need to build and push a container image from inside a cluster, a build pod/container should use the lighter-weight, lower-level `ocimage` commands instead:

```sh
ocimage build -t apps/api:v1 .
ocimage push apps/api:v1 default-registry.local/apps/api:v1
```

The first command writes the image to the pod's ocimage store (which can be overridden using `OCIMAGE_STORE` env var). The second command pushes that stored image to the cluster registry under the same `apps/` namespace that `podplane build` uses locally.

If the build pod should publish to a different local Podplane registry host, push to that host explicitly:

```sh
ocimage push apps/api:v1 other-registry.local/apps/api:v1
```

Keep user-built app images under `apps/`. The `mirror/` namespace is reserved for Podplane-managed dependency mirrors.
