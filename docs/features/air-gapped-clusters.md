---
title: "Support for running air-gapped clusters in offline/disconnected environments"
linkTitle: "Air-gapped Clusters"
weight: 120
description: "Prefetch dependencies and mirror images for operation without internet connectivity"
---

Podplane can prepare all dependencies a cluster needs while you still have network access, then use those cached dependencies in an isolated environment.

This makes dependency versions explicit and verifiable instead of relying on live downloads during every VM boot or cluster deployment.

It also mitigates boot fails and upstream repository rate-limiting, and speeds up boot times to support fast cluster auto-scaling.

## Download dependencies

`podplane deps download` fetches VM packages, component images, template charts, seed snapshots, and the platform components Git source.

Select every target architecture and provider that the disconnected environment will use:

```bash
podplane deps download \
  --arch amd64,arm64 \
  --providers aws \
  --addons all
```

VM bootstrap verifies downloaded artifacts against their published digests before installing them.

`podplane deps tf` separately creates a filesystem mirror for OpenTofu/Terraform providers and caches the exact Nstance module source.

## Mirror OCI images

Podplane renders known platform component and template images through the built-in registry:

```text
<registry>/mirror/<upstream-registry>/<repository>:<tag>
```

To simplify operations and make debugging easy, the use of mirrored images requires explicit URIs rather than using origin URIs with transparent pull-through cache.

Only images and CPU architectures downloaded in advance are available offline.

Fully disconnected operation requires planning: transport the dependency cache into the environment, provide the components Git/chart source internally, mirror application images, and make required identity, DNS, object-storage, and secrets endpoints reachable. The commands prepare the software supply chain; they cannot make external services available inside an air gap.

## Learn more

- [`podplane deps download` reference](../reference/cli/commands/deps-download.md)
- [`podplane deps tf` reference](../reference/cli/commands/deps-tf.md)
- [Registry mirror configuration](../reference/configuration.md)
