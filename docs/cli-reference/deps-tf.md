---
title: "deps tf"
weight: 30
description: "Download cluster OpenTofu/Terraform dependencies"
---

Downloads all OpenTofu/Terraform providers and modules used by generated cluster
infrastructure into `deps/tf/` in the Podplane dependency cache.

Provider packages are stored as a filesystem mirror, module source is normalized for
local use, and the resolved provider lock information is retained.

Downloads are additive, so provider packages and versioned modules already used by other
clusters remain available.

```shell
podplane deps tf [--platform OS_ARCH]...
```

`--platform` is repeatable. It defaults to the platform reported by the
selected OpenTofu/Terraform engine. Pass target platforms explicitly when
preparing dependencies for another environment, for example:

```shell
podplane deps tf --platform linux_amd64 --platform linux_arm64
```

Podplane uses `PODPLANE_TF_CMD` when set and otherwise selects `tofu`, then
`terraform`. This command requires public registry access. The resulting
`deps/tf/` directory can be persisted or packaged for offline jobs. Consume a
cache with the same engine and version used to create it.

Existing generated clusters do not require a separate dependency manifest.
Their generated module source path records the exact module version and their
`.terraform.lock.hcl` records provider versions. Running `cluster create` will
attempt to fetch any of those exact packages that are missing from the shared
cache if registry access is available. Running `cluster upgrade` instead
downloads the latest compatible versions and moves the cluster to them.

Cluster generation always uses the cache:

```shell
podplane cluster create --no-apply
```

That command references the exact cached Nstance module version, writes the
retained `.terraform.lock.hcl`, and creates
`deps/tf/podplane.tfrc` in the shared cache pointing to the provider
mirror. It does not copy provider, module, or CLI configuration files into the
cluster directory. Podplane automatically uses the shared configuration when
it runs `init`. To invoke the engine manually without public registry access,
set `TF_CLI_CONFIG_FILE` to that file in the configured dependency cache:

```shell
TF_CLI_CONFIG_FILE="/path/to/dependency-cache/tf/podplane.tfrc" tofu init
```

Replace `tofu` with `terraform` when Terraform was selected while creating the
cache.
