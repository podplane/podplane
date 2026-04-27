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

The flow for AWS or Google Cloud is:
1. `podplane cluster create`
    1. generates a `podplane.cluster.jsonc` config file.
    2. generates OpenTofu/Terraform `.tf` files.
    3. optionally, can deploy the infrastructure as well.
2. A Podplane provider for OpenTofu/Terraform invokes the CLI to generate Netsy snapshot files and uploads them to object storage (S3 for AWS, GCS for Google Cloud).
3. Nstance auto-scales cluster VMs using the Podplane userdata script.

## Dependencies

### Infrastructure

- [opentofu](https://opentofu.org/docs/intro/install/) OR [terraform](https://developer.hashicorp.com/terraform/install) to deploy to AWS or Google Cloud
- [nstance-server](https://nstance.dev/docs/components/nstance-server/) to run VMs

### Auth

If you do not have an existing OIDC server:

- [easy-oidc](https://easy-oidc.dev), a minimal OIDC server for Google and GitHub cluster authentication
