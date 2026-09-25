---
title: "Operate and upgrade a cluster"
weight: 80
description: "Manage configuration, components, infrastructure versions, and cluster lifecycle"
---

Keep the cluster configuration, generated OpenTofu/Terraform files, provider lock, and pinned VM dependency manifests in version control. Review changes as infrastructure changes, even when Podplane generated them.

## Change cluster configuration

Edit `podplane.cluster.jsonc`, regenerate without applying, and review the resulting plan:

```bash
podplane cluster create --no-apply
podplane cluster create
```

Runtime settings can be pushed to existing VMs. Changes such as an instance type or VM image cause Nstance to replace nodes gradually. Cloud-resource changes follow the OpenTofu/Terraform plan. See [Configuration Impact](../reference/infrastructure.md#configuration-impact) before applying production changes.

## Manage components

```bash
podplane install metrics-server
podplane uninstall metrics-server
```

Flux reconciles component changes in the cluster. Core components cannot be uninstalled, and CRD charts are retained to avoid deleting custom resource data.

## Upgrade infrastructure dependencies

Ordinary `cluster create` runs keep the module and provider versions already recorded by the stack. Upgrade them deliberately:

```bash
podplane cluster upgrade --no-apply
podplane cluster upgrade
```

Review the generated files and provider plan before applying. Nstance handles rolling VM replacement when an upgrade changes VM infrastructure.

## Remove a cluster

`podplane cluster delete` scales managed groups down before running the infrastructure destroy. It leaves the cluster configuration and generated files in place. Treat deletion as destructive and confirm that any application data stored outside Podplane-managed state has its own retention or backup plan.

See the [`cluster delete` reference](../reference/cli/commands/cluster-delete.md) for the exact behavior and options.
