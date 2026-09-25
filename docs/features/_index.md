---
title: "Features"
weight: 20
description: "An overview of Podplane features"
---

Podplane is designed to make deploying a secure container platform easy, with the following features built-in:

- [Container Registry](container-registry.md) stores app and mirrored platform images in object storage for use in your cluster.
- [Secrets Management](secrets-management.md) stores encrypted secrets in your preferred backend and securely mount them into workloads.
- [Workload Certificates](workload-certificates.md) automatically generates and rotates server certificates and SPIFFE SVID client certificates directly in Pods.
- [Ingress Certificates](ingress-certificates.md) automatically generates and rotates wildcard certificates for your ingress domains with ACME e.g. Let's Encrypt.
- [Deployment Templates](deployment-templates.md) make it easy to deploy common workload types with one command and an opinionated Helm chart.
- [IAM Roles for Pods](iam-roles-for-pods.md) gives AWS workloads the ability to use AssumeRole to use narrowly scoped IAM identities through kube2iam.
- [Local Dev Clusters](local-dev-clusters.md) let you quickly run a single-node Podplane cluster in a local VM.
- [Secure Authentication](secure-authentication.md) uses OIDC for people, services, and CI jobs to securely access your cluster.
- [Role-Based Access Control](role-based-access-control.md) provides useful built-in roles and group bindings for assigning access to users, instead of having to create every role and binding yourself.
- [Cluster State in Object Storage](cluster-state-in-object-storage.md) enables support for scaling clusters to zero and makes cluster state backup easy thanks to [Netsy](https://netsy.dev).
- [Cluster Auto-scaling](cluster-auto-scaling.md) enables scaling clusters from zero to many nodes, and supports on-demand and spot instances, thanks to [Nstance](https://nstance.dev).
- [Air-Gapped Support](air-gapped-clusters.md) makes it possible to run clusters offline, thanks to dependency and image mirroring to local directories or object storage.
- [Multiple Cloud Providers](cloud-providers/_index.md) are supported, from public clouds such as AWS and Google Cloud, to private cloud/on-prem with Proxmox support.
