---
title: "Useful Kubernetes roles out of the box"
linkTitle: "Role-based Access"
weight: 90
description: "Start with clear access levels for administrators, operators, developers, and viewers"
---

Kubernetes includes powerful RBAC roles, but it does not decide which groups in your identity provider should receive them. 

Podplane supplies a small, sensible set of group bindings:

| Group | Access |
| --- | --- |
| `podplane:admins` | Unrestricted cluster administration |
| `podplane:managers` | Administration within namespaces |
| `podplane:operators` | Workload editing and shell access |
| `podplane:editors` | Workload editing without Pod shell access |
| `podplane:viewers` | Read-only access |

These groups map to Kubernetes' built-in `cluster-admin`, `admin`, `edit`, and `view` roles.

An admission policy distinguishes editors from operators by denying `pods/exec` and `pods/attach` to editors unless they also belong to a higher group.

Your OIDC provider remains the source of group membership, and Kubernetes remains the authorization source. Podplane standardises useful names and installs the bindings; it does not replace Kubernetes RBAC or stop you from defining narrower roles.

The default bindings come from the `platform-rbac` component and are present in seeded clusters. A bare cluster created without platform components does not install them automatically.

## Learn more

- [Podplane RBAC guide](../guides/rbac.md)
- [Component management](../reference/components.md)
- [Kubernetes RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/)
