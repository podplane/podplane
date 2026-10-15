---
title: "Useful Kubernetes roles out of the box"
linkTitle: "Role-based Access"
weight: 90
description: "Start with clear access levels for administrators, operators, developers, and viewers"
---

Kubernetes includes powerful RBAC roles, but it does not decide which groups in your identity provider should receive them. Podplane supplies a small, sensible set of group bindings, and delineates rules on platform vs tenant namespaces.

Tenant namespaces are every namespace without a `kube-` or `platform-` prefix. Those prefixes are reserved for the platform, which only admins can access.

The default group bindings are:

| Group | Access |
| --- | --- |
| `podplane:admins` | Unrestricted cluster administration |
| `podplane:managers` | Administration of tenant namespaces |
| `podplane:operators` | Workload editing and shell access in tenant namespaces |
| `podplane:editors` | Workload editing without Pod shell access in tenant namespaces |
| `podplane:viewers` | Read-only access to tenant namespaces |

These groups map to Kubernetes' built-in `cluster-admin`, `admin`, `edit`, and `view` roles.

Admins can restrict a tenant namespace to grant access explicitly instead of automatically, and organisations that need finer control can skip the standard groups and bind their own.

The admins binding comes from the `platform-rbac` component. Access for the other groups comes from the `podplane-operator` component, so bare and minimal clusters are admin-only until it is installed.

An admission policy distinguishes editors from operators by denying `pods/exec` and `pods/attach` to editors unless they also belong to a higher group.

Your OIDC provider remains the source of group membership, and Kubernetes remains the authorization source. Podplane standardises useful names and installs the bindings; it does not replace Kubernetes RBAC or stop you from defining narrower roles.

## Learn more

- [Podplane RBAC guide](../guides/rbac.md)
- [Component management](../reference/components.md)
- [Kubernetes RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/)
