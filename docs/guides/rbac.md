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

Podplane provides default group bindings for these built-in roles. Admins get access to the whole cluster; every other group gets access only to tenant (non-platform) namespaces.

## Default Groups

| Group | Purpose | Kubernetes role | Scope |
| --- | --- | --- | --- |
| `podplane:admins` | Unrestricted cluster administration. | `cluster-admin` | Cluster |
| `podplane:managers` | Namespace administration, including granting access with RoleBindings. | `admin` | Tenant namespaces |
| `podplane:operators` | Workload operations, including shell access to running pods. | `edit` | Tenant namespaces |
| `podplane:editors` | Workload editing without shell access to running pods. | `edit` plus admission policy denying `pods/exec` and `pods/attach` | Tenant namespaces |
| `podplane:viewers` | Read-only access. | `view` | Tenant namespaces |

`podplane:operators` and `podplane:editors` both map to Kubernetes `edit`. The distinction is enforced by `platform-rbac` admission policy: operators may use pod shell access, while editors may not unless they also belong to a higher shell-capable group.

## Tenant and Platform Namespaces

Namespaces prefixed `kube-` or `platform-` are "platform" namespaces. They hold
Kubernetes and Podplane components, and only admins can access them. Only
admins and the platform's Flux installation can create namespaces with these
prefixes. Every other namespace is a "tenant" namespace.

The defaults protect the platform from users, not teams from each other: every
non-admin group gets its role in every tenant namespace, including new ones. To
isolate a team, restrict its namespaces.

Admins can grant access to a specific platform namespace, for example for
troubleshooting, with an explicit RoleBinding.

## Restricting a Namespace

A restricted namespace gets no default group access. To grant access explicitly to Podplane built-in roles, label a namespace as restricted:

```sh
kubectl label namespace team-a rbac.podplane.dev/restricted=true
kubectl create rolebinding team-a-editors --namespace team-a \
  --clusterrole edit --group team-a:editors
```

Only admins can label namespaces. Removing the label restores default access.

## Custom Groups

If the default groups do not fit, do not emit them in your OIDC tokens. Bind
your own groups to roles with RoleBindings, or with ClusterRoleBindings for
cluster-wide access.

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

The `platform-rbac` [component](../reference/components.md) installs the
`podplane:admins` ClusterRoleBinding and the admission policies.

The `podplane-operator` component keeps a RoleBinding for each other group in
every tenant namespace that is not restricted. It manages only RoleBindings
labelled `rbac.podplane.dev/managed: "true"`, and recreates them if they are
deleted. Disable it with the chart value
`podplane.operator.config.rbac.namespaceGroupBindings: false`.

Bare and minimal clusters do not include `podplane-operator`, so they are
admin-only until it is installed:

```sh
podplane install podplane-operator
```

Kubernetes remains the authorization source for API access; Podplane only
standardises the group names and default bindings.
