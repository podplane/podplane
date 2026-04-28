---
title: "CLI Overview"
weight: 70
description: "Podplane CLI design and command overview"
---

# Podplane CLI

## Terminology

- **Components** are platform-level services managed via `install` / `uninstall`. Components are either __core__ components (always installed, cannot be removed e.g. CoreDNS, Cilium) or __addon__ components (can be installed and uninstalled e.g. Traefik, metrics-server).
- **Apps** are your own workloads, deployed and removed via `deploy` / `remove` using app __templates__ (e.g. `web` or `worker` template).
- Both templates and components can have configurable __features__.

## Command Groups

The Podplane CLI can be divided into groups of commands:

- `cluster` for managing Podplane clusters
- `oidc` for managing Easy OIDC deployments
- authentication commands
- `hooks` for integration e.g. kubectl exec auth plugin, podplane TF providers for Netsy state initialization
- app commands for deploying and removing apps using templates
- component commands for managing addon components
- `local` for managing local VM clusters
- `package` for managing local VM dependency packages
- informational commands

Each CLI command group is summarised below.

## Config Files & Context

Many commands require either a cluster config file, or OIDC config file.

- For example, `podplane login` will require a cluster config file, and `podplane oidc delete` will require an oidc config file.

The CLI uses the current working directory to find the relevant `podplane.cluster.jsonc` or `podplane.oidc.jsonc` config file. See [Config Reference](config-reference.md) for the full file format documentation.

- Alternatively, you can specify a config file path using the `-f` flag e.g. `podplane login -f ./my-cluster/podplane.cluster.jsonc`

We recommend setting up a Git repository for storing all of your cluster and OIDC server infrastructure-as-code, for example:

```
├── infra/                                        # git repo
│   │
│   ├── auth-production/                          # example Easy OIDC server
│   │   ├── podplane.oidc.jsonc                   # config file
│   │   └── podplane.*.tf                         # generated .tf files
│   │
│   └── internaltools-production/                 # example cluster
│       ├── podplane.cluster.jsonc                # config file
│       ├── podplane.*.tf                         # generated .tf files
│       └── custom.tf                             # optional custom infrastructure
```

## Commands Summary

### `cluster` commands

- `create` generates or reads a cluster config file, generates infra-as-code files, and (for AWS/Google Cloud) deploys the cluster via OpenTofu/Terraform
- `delete` for (on AWS/Google Cloud) removing deployed infrastructure and deleting generated files created by `create`.

### `oidc` commands

- `create` generates or reads an OIDC config file, generates infra-as-code files, and (for AWS/Google Cloud) deploys the OIDC via OpenTofu/Terraform
- `delete` for (on AWS/Google Cloud) removing deployed infrastructure and deleting generated files created by `create`.

### authentication commands

- `login` for authenticating via kubectl using the auth URL specified in the cluster configuration file
- `logout` to remove the previously authenticated cluster from your kubeconfig via kubectl

### `hooks` commands

- `kubectl-auth` to be used as a kubectl exec auth plugin
- `netsy-init` to generate an initial Netsy snapshot file from a template; called by the Podplane Terraform/OpenTofu providers (one binary for AWS, one for Google Cloud) during cluster creation

### app commands

These commands help you deploy workloads using templates such as the `web` or `worker` app template.

- `deploy <template> --name <name> --image <image>` deploy an app using a template. The CLI will prompt to install addon components if they have required dependencies which are not installed.
- `remove <template> --name <name>` remove a previously deployed app.
- `logs <name>` tail logs for a deployed app.

The `deploy` and `remove` commands are convenience functions which wrap `helm` commands. The `logs` command wraps `kubectl logs`.

### `install` / `uninstall` commands

Addon components extend your cluster's capabilities, such as Traefik ingress controller or CSI drivers.

- `install <component>` installs a component into the cluster with an opinionated, tested configuration.
- `uninstall <component>` removes a previously installed component from the cluster.

These components are installed and managed by Flux CD.

### `local` commands

You can run multiple single-node cluster VMs. If a name is omitted, `default` is used as the name.

- `start [name]` start a local cluster VM, and creates it if it doesn't exist
- `status [name]` report the status of a local cluster VM
- `stop [name]` stop a local cluster VM
- `delete [name]` delete a local cluster VM and its state files

The following commands exist primarily for Podplane development work on the `vmconfig` package:

- `shell [name]` open a shell into the local cluster VM or run a command via ssh
- `sync [name]` rsync files into the local cluster VM

The following command exists primarily for debugging:

- `server` runs a local background webserver that serves cached packages to VMs and hosts a fake OIDC server for local clusters

Note `server` is run automatically in the background when `local start` is used, and stopped on `local stop` of the last running VM.

### `package` commands

The `local` commands automatically download and cache packages. These commands exist primarily for debugging that cache.

- `status` reports current state of the cache and if any new package versions are available to download
- `download` force-downloads the latest package versions

### informational commands

- `version` reports the current CLI version
