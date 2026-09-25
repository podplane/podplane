---
title: "Set up team and CI access"
linkTitle: "Team and CI Access"
weight: 60
description: "Authenticate people and automation with OIDC and assign appropriate access"
---

Podplane uses OIDC for both human and service authentication. Your identity provider authenticates the caller and supplies group claims; Kubernetes RBAC decides what those groups may do.

## Team access

Assign people to the Podplane groups that match their responsibilities, then have them run:

```bash
podplane login -f podplane.cluster.jsonc
```

After sign-in, Podplane stores the credentials in the user's keyring and configures kubectl to use them. When a token expires, Podplane refreshes it automatically. Start with the least-privileged role that meets their needs (viewer, editor, operator, manager, or administrator) and grant greater access only when it is needed.

See [Role-based Access Control](rbac.md) for the exact groups and their Kubernetes permissions.

## CI access

[Truster](https://truster.dev) can exchange a short-lived GitHub Actions or Buildkite identity for a cluster token. Trust policies should restrict the allowed organisation, repository, branch, pipeline, and resulting groups.

For GitHub Actions, grant the job `id-token: write` and keep one random keyring password available to each Podplane step:

```bash
export PODPLANE_KEYRING_PASS="$(openssl rand -hex 32)"
podplane login --identity-provider github
podplane deploy web --name api --image "$IMAGE"
```

Store `PODPLANE_KEYRING_PASS` as an encrypted CI secret when commands run in separate steps, and never print it. Buildkite works the same way with `--identity-provider buildkite`.

For provider detection, identity files, token renewal, and required OIDC federation support, see the [`login` reference](../reference/cli/commands/login.md).
