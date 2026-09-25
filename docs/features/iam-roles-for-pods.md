---
title: "AWS IAM roles for Pods with kube2iam"
linkTitle: "IAM Roles for Pods"
weight: 60
description: "Give AWS workloads scoped cloud permissions without static credentials"
---

On AWS, Podplane runs [kube2iam](https://github.com/jtblin/kube2iam) on each cluster node. Pods can request an IAM role and receive temporary AWS credentials through the normal EC2 metadata interface, so applications do not need long-lived access keys in configuration or secrets.

This is useful for workloads that need direct access to services such as S3, SQS, DynamoDB, Secrets Manager, etc. Each workload can receive a role with only the permissions it needs instead of sharing the node's broader instance role.

## Guardrails included

Podplane configures the node networking and Kubernetes access kube2iam needs, uses regional AWS STS endpoints, and enables kube2iam's namespace restrictions. Kubernetes RBAC gives kube2iam read-only access to the Pod and Namespace metadata it uses for authorization.

Application IAM remains an infrastructure responsibility. Cluster operators create the IAM role and trust policy, decide which namespaces may use it, and annotate workloads according to kube2iam's role-mapping rules.

IAM roles for Pods are AWS-specific. Google Cloud and Proxmox use different workload identity mechanisms.

## Learn more

- [VM configuration](../reference/vm-configuration.md#package-dependencies) — software that runs on each Podplane node.
- [kube2iam usage and namespace restrictions](https://github.com/jtblin/kube2iam#kubernetes-namespace-restrictions)
- [AWS IAM roles](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles.html)
