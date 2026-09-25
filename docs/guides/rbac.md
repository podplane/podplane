---
title: "RBAC"
linkTitle: "Role-based Access Control"
weight: 70
description: "Podplane Kubernetes access groups and default RBAC"
aliases:
  - /docs/rbac/
---

# Role-based Access Control (RBAC)

Podplane uses OIDC for Kubernetes authentication. Tokens issued for users and
services include a `groups` claim that Kubernetes uses for authorization.

Kubernetes has multiple `ClusterRole` resources built-in, such as `cluster-admin`, `admin`, `edit`, and `view`. However, it does not define which external OIDC groups these roles map to.

Podplane provides default group bindings for these built-in roles. The `platform-rbac` component maps Podplane's default group names to the corresponding Kubernetes RBAC roles.

## Default Groups

| Group | Purpose | Kubernetes role |
| --- | --- | --- |
| `podplane:admins` | Unrestricted cluster administration. | `cluster-admin` |
| `podplane:managers` | Namespaced administration without full cluster-admin privileges. | `admin` |
| `podplane:operators` | Workload operations, including shell access to running pods. | `edit` |
| `podplane:editors` | Workload editing without shell access to running pods. | `edit` plus admission policy denying `pods/exec` and `pods/attach` |
| `podplane:viewers` | Read-only cluster access. | `view` |

`podplane:operators` and `podplane:editors` both map to Kubernetes `edit`. The distinction is enforced by `platform-rbac` admission policy: operators may use pod shell access, while editors may not unless they also belong to a higher shell-capable group.

## OIDC Requirements

Your OIDC issuer must emit the group names above in the token `groups` claim.
[Truster](https://truster.dev) can assign groups to users and trusted service
identities. Its trust policies should give CI workloads dedicated,
least-privilege groups rather than sharing a person's identity or granting
`podplane:admins`. You can instead bring another OIDC provider that supports
the same claims; Podplane service login additionally requires RFC 8693 token
exchange support.

Kubernetes usernames use the configured OIDC username claim without an issuer
prefix. The claim defaults to `sub`; Truster user logins use normalized email
subjects there, while trusted service identities use namespaced subjects
beginning with `trusted:`.

## Implementation

The `platform-rbac` [component](../reference/components.md) installs the default `ClusterRoleBinding` resources and admission policies. Kubernetes remains the authorization source for API access; Podplane only standardises the group names and default bindings.
