---
title: "Infrastructure"
weight: 30
description: "How Podplane provisions and manages cloud infrastructure"
---

# Infrastructure

The "Infrastructure Layer" of the Podplane [Architecture](architecture.md) is responsible for getting VMs scheduled in your cloud environment.

Podplane currently supports AWS, Google Cloud, and Proxmox VE.

## How It Works

For AWS and Google Cloud, this layer handles:
- Generating versionable infrastructure-as-code (OpenTofu/Terraform `.tf` files for AWS and Google Cloud)
- Deploying and managing cloud resources (VPCs, subnets, security groups, VMs, object storage) for AWS and Google Cloud

For Proxmox VE, this layer handles:
- Scripts for initialising Proxmox VE nodes

This layer also handles:
- Configuration for auto-scaling VMs via [Nstance](https://nstance.dev)
- VM userdata configuration for downloading package dependencies for each `vmconfig` VM "kind"
- Configuration for deploying an [Easy OIDC](https://easy-oidc.dev) server for cluster authentication (if you don't have an existing OIDC server)

## Provisioning Flow

The flow for AWS or Google Cloud is:
1. `podplane cluster create`
    1. generates a `podplane.cluster.jsonc` config file.
    2. generates OpenTofu/Terraform `.tf` files.
    3. optionally, can deploy the infrastructure as well.
2. The Podplane OpenTofu/Terraform provider creates a Netsy `bootstrap.netsy` snapshot file from a Podplane seed file using Podplane's `netsyseed` package, then uploads it to object storage (S3 for AWS, GCS for Google Cloud) using a conditional put to ensure it never overwrites existing cluster state.
3. Nstance auto-scales cluster VMs using the Podplane userdata script.

## Design Philosophy

Podplane's configuration aims to cover ~80% of infrastructure use cases. For the remaining 20%, the generated OpenTofu/Terraform files are designed to be extended with custom `.tf` files alongside them.

### Generated vs Custom Code

The CLI generates `podplane.cluster.*.tf` files alongside the `podplane.cluster.jsonc` config file. These files are fully managed by the CLI - `podplane cluster create` generates them and `podplane cluster upgrade` will regenerate them in the future. Users should never edit generated `.tf` files directly, instead tune the `podplane.cluster.jsonc` file, set generated variables in `terraform.tfvars` or `*.auto.tfvars`, or create additional custom `.tf` files.

Generated files prefer composition of published [Nstance Terraform modules](https://github.com/nstance-dev/nstance/tree/main/deploy/tf) (`cluster`, `account`, `network`, `shard`) over defining raw cloud resources. `podplane.cluster.main.tf` contains the Terraform/provider configuration, locals, module calls, and supporting resources. `podplane.cluster.variables.tf` contains generated variable declarations. `podplane.cluster.outputs.tf` contains generated outputs. `podplane.cluster.vmconfig.*.json` pins the vmconfig dependency manifests used to render VM userdata. `podplane.cluster.schema.json` contains a generated local JSON Schema referenced by `podplane.cluster.jsonc` so editors can provide validation, completion, and field documentation without internet access.

The pinned vmconfig manifest copies are Terraform inputs and may be edited/updated before planning to audit or override package versions, URLs, and checksums. Manifest changes appear in the Terraform plan through the `podplane_userdata` data source. Manifests are selected per VM pool architecture rather than the CLI host architecture, and `podplane cluster create` automatically fetches any required manifest missing from the local cache. Re-running the command replaces the copies with the currently cached manifests; use `podplane deps download --arch <architecture>` to refresh those cached versions first.

To set generated variables, create a user-owned `terraform.tfvars` or `*.auto.tfvars` file in the same directory. To add custom infrastructure (e.g. lifecycle rules on a bucket, additional IAM policies, extra cloud resources), create separate `.tf` files in the same directory. These files can reference outputs from the generated modules. The CLI will never modify files it didn't generate.

```
├── internaltools-production/
│   ├── podplane.cluster.jsonc              # cluster config
│   ├── podplane.cluster.schema.json        # generated - local editor schema
│   ├── podplane.cluster.main.tf            # generated - providers, locals, modules, resources
│   ├── podplane.cluster.variables.tf       # generated - variable declarations
│   ├── podplane.cluster.outputs.tf         # generated - outputs
│   ├── podplane.cluster.vmconfig.*.json    # generated - pinned userdata dependency manifests
│   ├── terraform.tfvars                    # user-owned variable values
│   └── custom.tf                           # your custom infrastructure
```

`podplane cluster upgrade` will regenerate all `podplane.cluster.*.tf` files and `podplane.cluster.schema.json` without touching any other files in the directory.

## Dependencies

### Infrastructure

- [opentofu](https://opentofu.org/docs/intro/install/) OR [terraform](https://developer.hashicorp.com/terraform/install) to deploy to AWS or Google Cloud
- [nstance-server](https://nstance.dev/docs/components/nstance-server/) to run VMs

### Auth

If you do not have an existing OIDC server:

- [easy-oidc](https://easy-oidc.dev), a minimal OIDC server for Google and GitHub cluster authentication
