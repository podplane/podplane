---
title: "CLI Reference"
weight: 80
description: "Podplane CLI reference"
---

## Cluster Commands

- [podplane cluster create](cluster-create.md) – Generate cluster configuration and deploy infrastructure.
- [podplane cluster delete](cluster-delete.md) – Remove deployed cluster infrastructure and generated files.

## OIDC Commands

- [podplane oidc create](oidc-create.md) – Generate OIDC configuration and deploy infrastructure.
- [podplane oidc delete](oidc-delete.md) – Remove deployed OIDC infrastructure and generated files.

## Authentication Commands

- [podplane login](login.md) – Authenticate to a cluster via kubectl.
- [podplane logout](logout.md) – Remove cluster authentication from kubeconfig.

## Hooks Commands

- [podplane hooks kubectl-auth](hooks-kubectl-auth.md) – kubectl exec auth plugin.
- [podplane hooks netsy-init](hooks-netsy-init.md) – Generate an initial Netsy snapshot file from a template.

## App Commands

- [podplane deploy](deploy.md) – Deploy an app using a template.
- [podplane remove](remove.md) – Remove a previously deployed app.
- [podplane logs](logs.md) – Tail logs for a deployed app.

## Component Commands

- [podplane install](install.md) – Install an addon component into the cluster.
- [podplane uninstall](uninstall.md) – Remove an addon component from the cluster.

## Local Commands

- [podplane local start](local-start.md) – Start a local cluster VM.
- [podplane local status](local-status.md) – Report the status of a local cluster VM.
- [podplane local stop](local-stop.md) – Stop a local cluster VM.
- [podplane local delete](local-delete.md) – Delete a local cluster VM and its state files.
- [podplane local shell](local-shell.md) – Open a shell into a local cluster VM.
- [podplane local sync](local-sync.md) – Sync files into a local cluster VM.
- [podplane local server](local-server.md) – Run a local background server for VMs.

## Package Commands

- [podplane package status](package-status.md) – Report package cache status.
- [podplane package download](package-download.md) – Download latest package versions.

## Informational Commands

- [podplane version](version.md) – Report the current CLI version.
