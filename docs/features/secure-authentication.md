---
title: "User and service authentication with OIDC and Truster"
linkTitle: "Secure Authentication"
weight: 80
description: "Use short-lived identities for people, automation, agents, and CI"
---

Podplane uses OpenID Connect (OIDC) for Kubernetes authentication.

People sign in through a browser; atuomation, agents, CI jobs, and other services exchange a short-lived workload identity.

In both cases, Kubernetes receives a verifiable token instead of a shared password or permanently issued admin credential/certificate.

To access a cluster, run:

```bash
podplane login
kubectl get pods
```

The CLI configures the kubeconfig exec plugin and renews tokens when needed.

CI identities such as GitHub Actions and Buildkite can be detected automatically, and a caller-managed OIDC token can be supplied by file.

## BYO OIDC issuer or deploy Truster

Existing OIDC providers can be used when they provide the username and group claims your cluster expects. Service login requires the OIDC provider to implement RFC 8693 token exchange.

If you do not already have a suitable OIDC issuer/server, Podplane can deploy [Truster](https://truster.dev).

Truster connects user identities from Google or GitHub and uses trust policies to map CI identities to narrowly scoped groups. Most organisations can share one carefully managed issuer across several clusters.

`podplane oidc create` currently deploys Truster infrastructure to AWS, with Google Cloud support coming soon.

Using an existing issuer is independent of the cluster's infrastructure provider.

## Learn more

- [`podplane login` reference](../reference/cli/commands/login.md)
- [`podplane oidc create` reference](../reference/cli/commands/oidc-create.md)
- [OIDC and cluster configuration](../reference/configuration.md)
- [Kubernetes OIDC authentication](https://kubernetes.io/docs/reference/access-authn-authz/authentication/#openid-connect-tokens)
