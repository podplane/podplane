---
title: "Guides"
weight: 10
description: "Install Podplane, deploy applications, and operate a cluster"
aliases:
  - /docs/getting-started/
---

These guides take you from installing the Podplane CLI, to deploying your apps on a local cluster, and then deploying them on AWS.

Follow each guide in order, or jump directly to the task you need:

1. [Install Podplane](installation.md) and its command-line dependencies.
2. [Deploy an app locally](local-cluster.md) in a single-node local cluster.
3. [Configure your app](configure-app.md) with templates, environment variables, secrets, logs, and routes.
4. [Manage application secrets](manage-secrets.md) securely with an external secrets provider.
5. [Create an AWS cluster](aws-cluster.md) and deploy the same application remotely.
6. [Set up team and CI access](team-and-ci-access.md) with OIDC authentication for people and automation.
7. [Role-based Access Control](rbac.md) explains Podplane's default groups and Kubernetes permissions.
8. [Pod Security](pod-security.md) explains Podplane's secure Pod defaults and how to relax them.
9. [Container Runtimes](container-runtimes.md) explains how Pods run in gVisor by default, and when to use runc.
10. [Operate and upgrade a cluster](operations.md) safely over time.
11. [Plan your infrastructure topology](topologies.md) as you add production, staging, CI/CD, and observability environments.

For a concise overview of what Podplane provides, see [Features](../features/_index.md).

For exact fields and command options, use the [Reference](../reference/_index.md).
