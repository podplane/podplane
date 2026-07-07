---
title: "CLI Reference"
weight: 80
description: "Podplane CLI reference"
---

## Global Options

These options are available on all commands:

| Flag | Description |
| --- | --- |
| `-v, --verbose` | Enable verbose output |
| `--version` | Show version information |

## Cluster Commands

- [podplane cluster create](cluster-create.md) – Generate cluster configuration and deploy infrastructure.
- [podplane cluster delete](cluster-delete.md) – Remove deployed cluster infrastructure.

## OIDC Commands

- [podplane oidc create](oidc-create.md) – Generate OIDC configuration and deploy infrastructure.
- [podplane oidc delete](oidc-delete.md) – Remove deployed OIDC infrastructure.

## Authentication Commands

- [podplane login](login.md) – Authenticate to a cluster via kubectl.
- [podplane logout](logout.md) – Remove cluster authentication from kubeconfig.

## Hooks Commands

- [podplane hooks kubectl-auth](hooks-kubectl-auth.md) – kubectl exec auth plugin.

## App Commands

- [podplane build](build.md) – Build a container image for your local Podplane VM.
- [podplane push](push.md) – Push a local image to the cluster registry.
- [podplane deploy](deploy.md) – Deploy an app using a template.
- [podplane remove](remove.md) – Remove a previously deployed app.
- [podplane secret](secret.md) – Manage application secrets through the Podplane operator.
- [podplane logs](logs.md) – Tail logs for a deployed app.

## Component Commands

- [podplane install](install.md) – Install an addon component into the cluster.
- [podplane uninstall](uninstall.md) – Remove an addon component from the cluster.

## Local Commands

- [podplane local start](local-start.md) – Start a local cluster VM.
- [podplane local status](local-status.md) – Report the status of a local cluster VM.
- [podplane local stop](local-stop.md) – Stop a local cluster VM.
- [podplane local delete](local-delete.md) – Delete a local cluster VM and its state files.
- [podplane local shell](local-shell.md) – Open a shell into a local cluster VM via SSH.
- [podplane local console](local-console.md) – Attach to a local cluster VM serial console.
- [podplane local sync](local-sync.md) – Sync files into a local cluster VM.
- [podplane local server](local-server.md) – Run a local background server for VMs.

## Deps Commands

- [podplane deps status](deps-status.md) – Report package cache status.
- [podplane deps download](deps-download.md) – Download latest package versions.

## Informational Commands

- [podplane version](version.md) – Report the current CLI version.
