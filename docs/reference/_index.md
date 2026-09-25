---
title: "Reference"
weight: 30
description: "Technical reference for Podplane"
---

Use the reference when you need exact CLI syntax, configuration options, or technical details about how Podplane works.

## CLI

- [Overview](cli/overview.md) — CLI terminology, command groups, configuration contexts, and file conventions.
- [Commands](cli/commands/_index.md) — syntax and options for every Podplane command.
- [Storage](cli/storage.md) — files and cached data that Podplane stores on your computer.

## Configuration

- [Configuration files](configuration.md) — cluster and OIDC configuration fields, defaults, and validation rules.

## Application deployments

- [Deployment Templates](templates.md) — resources, values, certificates, and image conventions for the built-in `web` and `worker` templates.
- [Secrets Management](secrets.md) — provider configuration, workload bindings, backend paths, and Kubernetes Secret sync.

## Architecture and internals

- [Architecture](architecture.md) — the layers and systems that make up Podplane.
- [Components](components.md) — optional and built-in software managed within a cluster.
- [Infrastructure](infrastructure.md) — how Podplane provisions and manages cloud resources.
- [VM configuration](vm-configuration.md) — how Podplane configures cluster nodes with `vmconfig`.
- [Seeds](seeds.md) — how seed files initialize Netsy cluster state.
