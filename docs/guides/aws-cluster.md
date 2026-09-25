---
title: "Create an AWS cluster"
weight: 50
description: "Generate, review, and deploy a Podplane cluster on AWS"
---

Podplane generates reviewable OpenTofu/Terraform for AWS instead of hiding cloud infrastructure behind an API. Before starting, complete the [installation guide](installation.md), configure an AWS profile or credentials, and choose an OIDC issuer for cluster login.

## Generate the cluster

Create a directory for the cluster configuration and generated infrastructure:

```bash
mkdir production && cd production
podplane cluster create --no-apply
```

Follow the prompts for the cluster name, AWS account, region, networking, VM pools, OIDC issuer, domains, secrets provider, and initial component seed. The default `recommended` seed is the best starting point for most clusters.

Commit `podplane.cluster.jsonc` and the generated files to a private infrastructure repository. Review the OpenTofu/Terraform plan before applying it:

```bash
podplane cluster create
```

Podplane asks for confirmation before applying unless `--auto-approve` is used.

## Log in and deploy

```bash
podplane login
kubectl get nodes
```

To promote an image you tested locally:

```bash
podplane push api:v1
podplane deploy web \
  --name api \
  --image registry.example.com/apps/api:v1 \
  --hostname api.example.com
```

Replace `registry.example.com` with `cluster.registry.hostname` from your generated configuration.

## Learn more

- [AWS feature overview](../features/cloud-providers/aws.md)
- [`cluster create` reference](../reference/cli/commands/cluster-create.md)
- [Cluster configuration](../reference/configuration.md)
- [Infrastructure and generated files](../reference/infrastructure.md)
