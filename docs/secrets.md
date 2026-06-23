---
title: "Secrets"
weight: 55
description: "How Podplane manages application secrets"
---

# Secrets

Podplane Secrets makes it easy for developers to store application secrets securely in an external secrets store using straightforward CLI commands, with tooling for securely mounting secrets into pods, and support for syncing to Kubernetes secrets for legacy applications which may require secrets to be set through environment variables.

It is designed for values such as database URLs/passwords, API tokens/keys/passwords, and provider credentials that should not be committed to Git, stored in Helm values, printed in shell history, or written into ordinary Kubernetes manifests or etcd/Netsy.

## How It Works

Podplane separates secret management into two paths:

1. __Writes__ store secrets in a configured provider such as AWS Secrets Manager, AWS Parameter Store, Google Secret Manager, Vault, or OpenBao. The `podplane secret` CLI commands encrypt values locally before sending them to the Podplane operator running in the cluster, which then handles persistence with a secrets provider.
2. __Reads__ are configured via a Custom Resource Definition (CRD) called `SecretProviderBinding`, which the Podplane operator uses to generate a `SecretProviderClass` that is used by the Secrets Store CSI Driver and so pods can mount provider-backed values as files.

CLI commands that list or return secrets show metadata only, and will never print secret values back to the terminal.

The steps for deploying a Pod with a secret are:

1. Use the Podplane CLI to store your secret in your secrets provider

    - e.g. `podplane secret create`

2. Create a `SecretProviderBinding` for the provider and one or more secret keys

    - The Podplane operator will generate a corresponding `SecretProviderClass`

3. Create a volume and volume mount on your `Pod` spec (e.g. `Deployment`) and deploy it
 
    - Secrets Store CSI Driver will handle mounting the secret per the `SecretProviderClass`

## Secrets Provider Backend Paths

Podplane stores secrets provider values under a stable namespace and binding boundary called a "keyspace":

```text
/<cluster-secrets-prefix>/<namespace>/<binding-name>/<key>
```

The exact provider representation varies by backend. For example, Google Secret Manager uses an alternative delimiter to slash-separated names, but preserves the same logical boundary.

Provider names, namespaces, binding names, and keys are intentionally restricted to simple DNS-label-like path segments so generated backend paths are predictable and do not contain arbitrary slashes.

## Create, Update, Delete, Restore, Destroy

Podplane treats secret lifecycle operations explicitly:

- `podplane secret create` only creates a missing active key. It fails if the key already exists or is archived.
- `podplane secret update` overwrites an existing active key and requires additional overwrite authorization.
- `podplane secret delete` archives a key when the provider supports recoverable deletion.
- `podplane secret restore` restores an archived key when supported.
- `podplane secret destroy` permanently removes provider data and requires separate destroy authorization.

Underlying provider behavior may not be identical, but Podplane aims to provide a consistent user experience. For example, AWS Parameter Store does not support recoverable archive and restore, so Podplane requires `destroy` for permanent deletion there. Google Secret Manager archive disables the active version, while destroy only removes disabled archived versions.

## SecretProviderBinding

Applications declare which provider-backed keys they need with a namespaced `SecretProviderBinding`.

The operator syncs/reconciles each `SecretProviderBinding` into a `SecretProviderClass` with the same name in the same namespace. Workloads then reference that `SecretProviderClass` through the Secrets Store CSI volume configuration as they otherwise would do normally.

The reason this abstraction exists is that it provides a convention-based approach to securing secrets leveraging standard Kubernetes RBAC and namespace primitives. By creating a layer above Secrets Store CSI Driver, it enables a provider-backed Kubernetes secrets pattern without impeding the use of more powerful Secrets Store CSI Driver features if a cluster operator chooses to enable access to those via RBAC control.

## Kubernetes Secrets Sync

The aim of the Podplane Secrets system design is to avoid persisting provider secret values into Kubernetes Secrets, as it greatly increases the attack surface for these sensitive values. Instead, workloads mount values directly from the secrets provider through the Secrets Store CSI driver.

However, some legacy applications may not be able to read secrets from files, and may require Kubernetes Secrets for example to be mounted as environment variables. `SecretProviderBinding.spec.syncToKubernetesSecrets` can ask the CSI driver to copy mounted provider values into Kubernetes Secret objects, using the feature from Secrets Store CSI Driver to [Sync as Kubernetes Secrets](https://secrets-store-csi-driver.sigs.k8s.io/topics/sync-as-kubernetes-secret). Please note that the CSI sync still depends on a workload continously mounting the CSI volume; it is not a standalone operator-created Secret.

This is useful for controllers or applications that only know how to read Kubernetes Secrets, but __it changes the security model__: values become persisted in Kubernetes/etcd and are readable by principals with Kubernetes Secret access in that namespace. For that reason, sync is disabled by default. A cluster operator must enable it in two places - first, the Podplane operator configuration:

```json
{
  "allowSyncToKubernetesSecrets": true
}
```

Then each namespace that is allowed to persist provider values into Kubernetes Secrets must opt in with:

```yaml
metadata:
  annotations:
    secrets.podplane.dev/allow-sync-to-kubernetes-secrets: "true"
```

If either gate is missing, the operator marks the binding as not ready and does not render `secretObjects` into the generated `SecretProviderClass`.

## Cluster Operator Responsibilities

Cluster operators configure the available secrets providers for the Podplane operator. The cluster config contains provider names and non-secret selection metadata only; provider credentials belong in the operator deployment or the cloud identity assigned to it.

In `podplane.cluster.jsonc`, secrets provider metadata lives under `cluster.secrets`:

```jsonc
{
  "cluster": {
    "secrets": {
      "default_provider": "aws-secrets-manager",
      "providers": {
        "aws-secrets-manager": {
          "kind": "aws",
          "object_type": "secretsmanager"
        }
      }
    }
  }
}
```

Operators should grant RBAC to the Podplane aggregated secrets API deliberately. Normal Kubernetes authorization controls who can read key metadata, create new values, overwrite existing values, restore archived values, and permanently destroy provider data.

## Learn More

- [podplane secret CLI reference](cli-reference/secret.md) - command syntax and flags.
- [Components](components.md) - installing addon components such as the Secrets Store CSI Driver.
