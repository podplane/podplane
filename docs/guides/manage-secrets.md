---
title: "Manage Application Secrets"
linkTitle: "Manage Application Secrets"
weight: 40
description: "How Podplane manages application secrets"
aliases:
  - /docs/secrets/
  - /docs/guides/secrets/
---

# Manage Application Secrets

Podplane Secrets makes it easy for developers to store application secrets securely in an external secrets store using straightforward CLI commands, with tooling for securely mounting secrets into pods, and support for syncing to Kubernetes secrets for legacy applications which may require secrets to be set through environment variables.

It is designed for values such as database URLs/passwords, API tokens/keys/passwords, and provider credentials that should not be committed to Git, stored in Helm values, printed in shell history, or written into ordinary Kubernetes manifests or etcd/Netsy.

## Tutorial

When you run `podplane local start` it will print an example deploy command:

```bash
podplane deploy web --name hello \
  --image default-registry.local/mirror/ghcr.io/podplane/hello:latest \
  --hostname hello.default.localhost
```

Let's build on that example to demonstrate how secrets work:

```bash
podplane secret create --for hello secure-message

podplane deploy web --name hello \
  --image default-registry.local/mirror/ghcr.io/podplane/hello:latest \
  --hostname hello.default.localhost \
  --secret secure-message \
  -e HELLO_MESSAGE='/var/run/podplane/secrets/secure-message'
```

Now when you view the hello app in your browser (the deploy command prints the URL), you will see the contents of `secure-message` on the page. Obviously this is for demonstration purposes, do not print secrets in production!

How do these commands work together? You create a new secret `secure-message`, and then you deploy the web template with a secret binding which mounts that secret value as a file at `/var/run/podplane/secrets/secure-message`. The environment variable for the demo `hello` container image allows you to pass an absolute file path to load a file as the contents of the hello message, which is what `-e HELLO_MESSAGE=/var/run/podplane/secrets/secure-message` does.

## How It Works

Podplane separates secret management into two paths:

1. __Writes__ store secrets in a configured provider such as AWS Secrets Manager, AWS Parameter Store, Google Secret Manager, Vault, or OpenBao. The `podplane secret` CLI commands encrypt values locally before sending them to the Podplane operator running in the cluster, which then handles persistence with a secrets provider.
2. __Reads__ are configured via a Custom Resource Definition (CRD) called `SecretProviderBinding`, which the Podplane operator uses to generate a `SecretProviderClass` that is used by the Secrets Store CSI Driver and so pods can mount provider-backed values as files.

CLI commands that list or return secrets show metadata only, and will never print secret values back to the terminal.

`podplane deploy --secret` configures the binding, service account, and read-only volume mount for applications deployed from a template. If you are configuring a custom workload or secrets provider, see the [secrets reference](../reference/secrets.md).

## Create, Update, Delete, Restore, Destroy

Podplane treats secret lifecycle operations explicitly:

- `podplane secret create` only creates a missing active key. It fails if the key already exists or is archived.
- `podplane secret update` overwrites an existing active key and requires additional overwrite authorization.
- `podplane secret delete` archives a key when the provider supports recoverable deletion.
- `podplane secret restore` restores an archived key when supported.
- `podplane secret destroy` permanently removes provider data and requires separate destroy authorization.

Underlying provider behavior may not be identical, but Podplane aims to provide a consistent user experience. For example, AWS Parameter Store does not support recoverable archive and restore, so Podplane requires `destroy` for permanent deletion there. Google Secret Manager archive disables the active version, while destroy only removes disabled archived versions.

## Advanced Configuration

Podplane mounts values directly from the configured provider by default, which avoids persisting them in Kubernetes cluster state. Kubernetes Secret sync is available for legacy applications, but it changes the security model because values are copied into Kubernetes Secrets. It is therefore disabled by default and requires explicit operator and namespace opt-ins.

See the [secrets reference](../reference/secrets.md) for provider configuration, backend paths, `SecretProviderBinding`, custom workloads, and Kubernetes Secret sync.

## Learn More

- [Secrets reference](../reference/secrets.md) — provider configuration, workload bindings, and Kubernetes Secret sync.
- [podplane secret CLI reference](../reference/cli/commands/secret.md) — command syntax and flags.
- [Components](../reference/components.md) — installing addon components such as the Secrets Store CSI Driver.
