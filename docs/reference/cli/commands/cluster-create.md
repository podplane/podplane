---
title: "cluster create"
weight: 10
description: "Generate cluster configuration and deploy infrastructure"
aliases:
  - /docs/cli-reference/cluster-create/
---

## Overview

Generates or reads a cluster config file, generates infrastructure-as-code files, and deploys the cluster on AWS or Google Cloud via OpenTofu/Terraform. When creating a new config interactively, the command asks for provider settings, optional DNS management (Route53 or Cloud DNS), and which initial platform components to seed: `none` for a bare cluster, `minimal`, `platform`, or `agentic` (default). Use the `platform` seed to provision a traditional PaaS, and the `agentic` seed to provision an Agentic PaaS. The cluster ID is suggested as a slug of the cluster name and can be edited. After the cluster questions, the command asks for the cluster's OIDC issuer: either an existing issuer URL, which must have an OIDC client whose ID is the cluster ID, or a new Truster server set up with [`podplane oidc create`](oidc-create.md), which registers that client for you.

For AWS, the command resolves the account ID through STS using the selected profile (or the default credential chain when no profile is set), then shows it for confirmation. If credentials are unavailable, you can enter the 12-digit account ID manually. The confirmed account is saved in the cluster config before deriving the remote-state bucket. Existing configs with no account follow the same process; `--auto-approve` accepts a successful lookup but fails if it cannot resolve the account. An explicitly configured account is kept. This lookup also runs with `--no-apply` when the account is missing.

Before OpenTofu/Terraform initializes, Podplane checks the customer-owned remote state bucket for the cluster. If it is absent, the command shows its name and asks before creating it. AWS state uses a private, encrypted, versioned S3 bucket with S3 lockfiles; Google Cloud state uses a private, versioned GCS bucket with uniform bucket-level access and public-access prevention. New buckets retain noncurrent state versions for 90 days by default. Existing buckets must be encrypted and private; Podplane warns but proceeds when they are not versioned, and does not require its lifecycle policy. Podplane generates the non-secret `podplane.cluster.tfbackend` alongside the managed `.tf` files; retain it with the stack so a fresh checkout can use the remote state. `--no-apply` writes that configuration but does not contact cloud storage or create a bucket.

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

Before preparing dependencies or generating Terraform for an apply, the command performs a read-only remote-state access check with a 30-second timeout. The check does not guarantee permission to create every infrastructure resource. `--no-apply` skips this check.

If Google reports expired application credentials or `invalid_rapt`, run `gcloud auth application-default login` and retry. This refreshes the application credentials used by Podplane, not just the separate `gcloud` CLI login. If `GOOGLE_APPLICATION_CREDENTIALS` is set, refresh the credentials referenced by that variable instead.

## Review and apply progress

Podplane initialises the stack, saves a plan, and asks you to review it before
applying that exact plan. Both OpenTofu and Terraform use `-json`
event streams. A plan with no changes skips apply. Saved plans are kept in a
private temporary directory and removed on exit.

See the [OpenTofu/Terraform plan-and-apply dashboard](../terminal-ui.md#opentofuterraform-plan-and-apply-dashboard) for the resource table,
approval controls, log navigation, progress display, and plain-output mode.

Ctrl-C requests a safe stop and waits for engine/state cleanup. Cancellation or
failure does not roll back already-applied changes. Inspect diagnostics, including
any state recovery instructions, before rerunning to generate a new plan. A state
change by another operation between plan and apply causes the saved plan to be
rejected; Podplane never silently replans after approval. Moves, imports, deferred
changes, and unknown action types currently require manual engine review instead
of being omitted from approval.

If the wizard creates a new OIDC server, it has its own review and
approval flow. Cancelling the subsequent cluster apply does not remove the
OIDC server or an already-created state bucket. Successful apply means infrastructure was applied, not that cluster readiness checks have passed.

## Options

| Flag | Description |
| --- | --- |
| `-f, --cluster-config string` | Path to the cluster config file (default: `podplane.cluster.jsonc` in the current directory) |
| `--no-apply` | Generate OpenTofu/Terraform files but do not run apply |
| `-y, --auto-approve` | Skip confirmation prompts and approve the saved infrastructure plan |

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
