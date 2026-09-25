---
title: "Podplane on Amazon Web Services (AWS)"
linkTitle: "AWS"
weight: 10
description: "Deploy a complete, open source Kubernetes Platform-as-a-Service (PaaS) to Amazon Web Services (AWS)"
---

AWS is fully supported by Podplane. `podplane cluster create` generates reviewable OpenTofu/Terraform files and can apply them to create the networking, IAM, object storage, load balancing, and Nstance infrastructure for a cluster.

## AWS Services

- **EC2 and Auto Scaling Groups (ASGs)** provide Nstance server and cluster VM capacity.
- **S3** stores Netsy cluster state, registry images, logs, and other configured buckets.
- **IAM** gives Podplane services narrowly scoped access and supports application roles through kube2iam.
- **Network Load Balancers (NLBs) - optionally** expose the Kubernetes API and ingress data plane.
- **Route 53 - optionally** provides DNS records and ACME DNS-01 validation for apex and wildcard ingress certificates.
- **Secrets Manager or Parameter Store** can back Podplane Secrets and the workload certificate authority key.

## OpenTofu/Terraform

Generated infrastructure is normal OpenTofu/Terraform. Podplane owns files prefixed with `podplane.cluster.`; you can add separate `.tf` files and variable files for organisation-specific controls without having them overwritten.

Provider credentials stay on the machine running OpenTofu/Terraform. Cluster workloads receive service-specific IAM roles rather than inheriting the credentials used to create the cluster.

## Learn more

- [`podplane cluster create` reference](../../reference/cli/commands/cluster-create.md)
- [Infrastructure reference](../../reference/infrastructure.md)
- [Cluster configuration](../../reference/configuration.md)
- [IAM Roles for Pods](../iam-roles-for-pods.md)
