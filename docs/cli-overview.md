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
- `deps` for managing local VM dependency packages
- informational commands

Each CLI command group is summarised below.

## Config Files & Context

Podplane commands use different config/context sources depending on the command:

- __Cluster Config__
  Flags: `-f` / `--cluster-config`, default `./podplane.cluster.jsonc`
  For Commands: `cluster *`, `login`

- __OIDC Server Config__
  Flags: `-f` / `--oidc-config`, default `./podplane.oidc.jsonc`
  For Commands: `oidc *`

- __Kubernetes Context__ 
  Flags: `--context` / `--kubeconfig`, default `kubectl config current-context`
  For Commands: `deploy`, `remove`, `logs`, `install`, `uninstall`, `logout`.
  Exceptions:
  - `logout` optionally also accepts `--cluster` or `-f` / `--cluster-config`

- __No Context/Not Applicable__
  For Commands: `local *`, `deps *`, `version`, `completion`, `help`
  Exceptions:
  - `deps download` optionally accepts `--cluster-config` for auto-detecting providers
  - `local *` commands use `--id` to select the local cluster, defaulting to `default`

The CLI uses the current working directory to find the relevant `podplane.cluster.jsonc` or `podplane.oidc.jsonc` config file. See [Config Reference](config-reference.md) for the full file format documentation.

- Alternatively, you can specify a config file path using the `-f` flag e.g. `podplane login -f ./my-cluster/podplane.cluster.jsonc`

`podplane login` also caches a cluster summary used by later kube-context commands. For example, `podplane deploy` uses the cached summary to know whether the selected cluster has a registry mirror. If deploy reports a missing cluster summary, run `podplane login -f <podplane.cluster.jsonc>` for that cluster.

We recommend setting up a Git repository for storing all of your cluster and OIDC server infrastructure-as-code, for example:

```
├── infra/                                        # git repo
│   │
│   ├── auth-production/                          # example Easy OIDC server
│   │   ├── podplane.oidc.jsonc                   # config file
│   │   ├── podplane.oidc.schema.json             # generated local editor schema
│   │   └── podplane.*.tf                         # generated .tf files
│   │
│   └── internaltools-production/                 # example cluster
│       ├── podplane.cluster.jsonc                # config file
│       ├── podplane.cluster.schema.json          # generated local editor schema
│       ├── podplane.*.tf                         # generated .tf files
│       └── custom.tf                             # optional custom infrastructure
```

## CLI Storage

Podplane also stores CLI-owned files outside your project directories. These
files are separate from project configuration files such as
`podplane.cluster.jsonc` and `podplane.oidc.jsonc`.

Podplane classifies CLI-owned storage into XDG-aligned categories:

- **Config**: long-term config/auth metadata; meaningful to back up.
- **Cache**: downloaded or derived files that are safe to delete.
- **Data**: durable local VM / local-cluster files.
- **Runtime**: ephemeral process metadata; recreated by restarting Podplane processes.

See [CLI Storage](cli-storage.md) for more details about these files and how they are stored.

## Commands Summary

### `cluster` commands

- `create` generates or reads a cluster config file, generates infra-as-code files, and (for AWS/Google Cloud) deploys the cluster via OpenTofu/Terraform
- `delete` removes deployed infrastructure and leaves config and generated `.tf` files in place.

### `oidc` commands

- `create` generates or reads an OIDC config file, generates infra-as-code files, and (for AWS/Google Cloud) deploys the OIDC via OpenTofu/Terraform
- `delete` removes deployed infrastructure and leaves config and generated `.tf` files in place.

### authentication commands

- `login` for authenticating via kubectl using the auth URL specified in the cluster configuration file
- `logout` to remove the previously authenticated cluster from your kubeconfig via kubectl

### `hooks` commands

- `kubectl-auth` to be used as a kubectl exec auth plugin

### app commands

These commands help you deploy workloads using templates such as the `web` or `worker` app template.

- `build [PATH] [-t <image>]` builds an OCI image with ocimage and stores it in the local Podplane registry so a local VM can use it immediately. If no `Containerfile` or `Dockerfile` exists, Podplane can generate a conservative `Containerfile` for supported project types.
- `push <local-image> [<remote-image>]` pushes an image from the local Podplane registry to the current cluster registry. If the image is not cached but exists in Docker, Podplane can import and push it after confirmation.
- `deploy <template> --name <name> [--image <image>] [-e KEY=value]` deploy an app using a template. The CLI will prompt to install addon components if they have required dependencies which are not installed. Repeat `-e` / `--env` to set non-secret environment variables on the app container. If `--image` is omitted, the template default image is used.
- `remove --name <name>` remove a previously deployed app.
- `secret <command> --for <secret-provider-class-name>` create, update, list, archive, restore, and destroy application secret values through the Podplane operator. Values are encrypted locally before they are sent to Kubernetes.
- `logs <name>` tail logs for a deployed app.

The `build` command packages prebuilt files into OCI images without requiring Docker. The `deploy` and `remove` commands are convenience functions which wrap `helm` commands. The `logs` command wraps `kubectl logs`.

### `install` / `uninstall` commands

Addon components extend your cluster's capabilities, such as Traefik ingress controller or CSI drivers.

- `install <component>` installs a component into the cluster with an opinionated, tested configuration.
- `uninstall <component>` removes a previously installed component from the cluster.

These components are installed and managed by Flux CD.

### `local` commands

You can run multiple single-node cluster VMs. Use `--id` to select a cluster; when omitted, `default` is used.

- `start` start a local cluster VM, and creates it if it doesn't exist
- `status` report the status of a local cluster VM
- `stop` stop a local cluster VM
- `delete` delete a local cluster VM and its state files

The following commands exist primarily for Podplane development work on the `vmconfig` package:

- `shell [command]` open a shell into the local cluster VM or run a command via SSH
- `console` attach to the local cluster VM serial console for boot/login debugging; press `Ctrl-]` to detach
- `sync [from] [to]` rsync files into the local cluster VM

The following command exists primarily for debugging:

- `server` runs a local background webserver that serves cached packages to VMs and hosts a fake OIDC server for local clusters

Note `server` is run automatically in the background when `local start` is used, and stopped on `local stop` of the last running VM.

### `deps` commands

The `local` commands automatically download and cache dependencies. These commands exist primarily for debugging that cache.

- `status` reports current state of the cache and if any new dependency versions are available to download
- `download` force-downloads the latest dependency versions

### informational commands

- `version` reports the current CLI version
