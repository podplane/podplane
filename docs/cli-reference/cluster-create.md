---
title: "cluster create"
weight: 10
description: "Generate cluster configuration and deploy infrastructure"
---

## Overview

Generates or reads a cluster config file, generates infrastructure-as-code files, and (for AWS/Google Cloud) deploys the cluster via OpenTofu/Terraform. When creating a new config interactively, the command asks which initial platform components to seed: `recommended` (default), `minimal`, or `none` for a bare cluster.

The generated `podplane.cluster.vmconfig.*.json` files pin the dependencies used by the VM cloud-init scripts. The Podplane Terraform provider loads those JSON files before it renders VM userdata. Manifests are selected using each configured VM pool's architecture, independently of the machine running the CLI. If a required manifest is not cached, this command fetches it automatically. To update an already cached manifest, run `podplane deps download --arch <architecture>`, then re-run this command to update the relevant pinned copy.

```
podplane cluster create [flags]
```

Podplane uses OpenTofu when both `tofu` and `terraform` are available. Set
`PODPLANE_TF_CMD=terraform` to select Terraform explicitly. The value
may be an executable name on `PATH` or an executable path.

The command stores OpenTofu/Terraform providers and modules in Podplane's
shared dependency cache. Missing dependencies are downloaded automatically.
Generated module sources reference an exact version in that cache, and the
cluster's `.terraform.lock.hcl` records its provider versions.

## Options

| Flag | Description |
| --- | --- |
| `-f, --cluster-config string` | Path to the cluster config file (default: `podplane.cluster.jsonc` in the current directory) |
| `--no-apply` | Generate OpenTofu/Terraform files but do not run apply |
| `-y, --auto-approve` | Skip confirmation prompts and pass auto-approval to OpenTofu/Terraform |

Provider installation is restricted to the shared filesystem mirror through
`deps/tf/podplane.tfrc` in the dependency cache. No provider, module, or CLI
configuration files are copied into the cluster directory. For manual engine
commands, set `TF_CLI_CONFIG_FILE` to the shared configuration file.

For an existing stack, the generated module source path and
`.terraform.lock.hcl` record its exact module and provider versions. If any of
those packages are missing from the shared cache, this command retrieves them
automatically when registry access is available; it does not silently upgrade
the stack to the cache's current versions. Run `podplane cluster upgrade` to
intentionally upgrade the stack's modules and providers.
