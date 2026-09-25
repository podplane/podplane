---
title: "Secrets"
linkTitle: "Secrets Management"
weight: 27
description: "Secrets providers, workload bindings, backend paths, and Kubernetes Secret sync"
---

Podplane stores application secrets in an external provider and mounts them into workloads through the [Secrets Store CSI Driver](https://secrets-store-csi-driver.sigs.k8s.io/). This reference covers provider configuration and the Kubernetes resources behind the simpler `podplane secret` and `podplane deploy --secret` workflow.

## Secrets Provider Backend Paths

Podplane stores secrets provider values under a stable namespace and binding boundary called a "keyspace":

```text
/<cluster-secrets-prefix>/<namespace>/<binding-name>/<key>
```

The exact provider representation varies by backend. For example, Google Secret Manager uses an alternative delimiter to slash-separated names, but preserves the same logical boundary.

Provider names, namespaces, binding names, and keys are intentionally restricted to simple DNS-label-like path segments so generated backend paths are predictable and do not contain arbitrary slashes.

## SecretProviderBinding

Applications declare which provider-backed keys they need with a namespaced `SecretProviderBinding`.

The operator reconciles each `SecretProviderBinding` into a `SecretProviderClass` with the same name in the same namespace. Workloads then reference that `SecretProviderClass` through the Secrets Store CSI volume configuration.

This abstraction provides a convention-based approach to securing secrets with standard Kubernetes RBAC and namespace primitives. It enables provider-backed Kubernetes secrets without preventing a cluster operator from granting access to more advanced Secrets Store CSI Driver features.

By default, Podplane's operator chart installs a Kubernetes `ValidatingAdmissionPolicy` for Pods that use Secrets Store CSI volumes. Those Pods must set an explicit, non-empty `spec.serviceAccountName`, and every Secrets Store CSI volume on the Pod must reference a `secretProviderClass` with the same name as that service account. Pods that omit `serviceAccountName` are rejected instead of falling back to Kubernetes' `default` service account for this policy.

Templates configure these resources automatically when you use `podplane deploy --secret`. Custom workloads must create the binding, service account, CSI volume, and read-only volume mount explicitly.

For a custom workload:

1. Store the value in your configured provider with `podplane secret create`.
2. Create a `SecretProviderBinding` for the provider and keys the workload needs. The operator generates a `SecretProviderClass` with the same name.
3. Create a dedicated service account with that name and set it explicitly as the Pod's `spec.serviceAccountName`.
4. Add a Secrets Store CSI volume that references the generated `SecretProviderClass`, then mount it read-only into the workload.

## Kubernetes Secrets Sync

Podplane avoids persisting provider secret values into Kubernetes Secrets by default. Instead, workloads mount values directly from the external provider through the Secrets Store CSI Driver.

Some controllers and legacy applications can only consume Kubernetes Secrets or environment variables. `SecretProviderBinding.spec.syncToKubernetesSecrets` can ask the CSI driver to copy mounted provider values into Kubernetes Secret objects using [Sync as Kubernetes Secrets](https://secrets-store-csi-driver.sigs.k8s.io/topics/sync-as-kubernetes-secret). The sync still depends on a workload continuously mounting the CSI volume; it is not a standalone operator-created Secret.

This changes the security model: values become persisted in Kubernetes cluster state and are readable by principals with Kubernetes Secret access in that namespace. For that reason, sync is disabled by default and requires two opt-ins.

First, enable it in the Podplane operator configuration:

```json
{
  "allow_sync_to_kubernetes_secrets": true
}
```

Then opt in each namespace that may persist provider values into Kubernetes Secrets:

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
          "key_prefix": "shared-secrets",
          "object_type": "secretsmanager"
        },
        "openbao": {
          "kind": "openbao",
          "address": "https://bao.example.com",
          "mount_path": "secret",
          "auth_path": "auth/kubernetes",
          "operator_role": "podplane-operator"
        }
      }
    }
  }
}
```

`key_prefix` is optional per provider and defaults to `cluster.id`; set it when multiple clusters should intentionally share a backend prefix.

Vault/OpenBao providers use Kubernetes/JWT auth, the same mechanism used by the Secrets Store CSI providers for workloads. The operator authenticates with its own Kubernetes service account token using `auth_path` (default `auth/kubernetes`) and `operator_role` (default `podplane-operator`). The workload read path authenticates separately as the workload Pod service account through the generated `SecretProviderClass` `roleName`.

Vault and OpenBao addresses must use HTTPS. `ca_cert` may be used for endpoints served by a private CA. Local Podplane clusters set this for the local fakevault endpoint automatically.

Before creating a managed cluster that uses Vault or OpenBao as its default provider, provision one unencrypted PKCS#8 Ed25519 private key in the KV-v2 field `value` at `<mount_path>/data/<key_prefix>/workload-ca-key`. `mount_path` defaults to `secret`, and `key_prefix` defaults to `cluster.id`. Grant the operator role read-only access to that exact path. Podplane does not require a Vault/OpenBao token outside the cluster.

Cluster admins should grant RBAC to the Podplane aggregated secrets API deliberately. Normal Kubernetes authorization controls who can read key metadata, create new values, overwrite existing values, restore archived values, and permanently destroy provider data.

## Learn More

- [Manage application secrets](../guides/manage-secrets.md) — create a secret and mount it into an application.
- [`podplane secret` CLI reference](cli/commands/secret.md) — command syntax and flags.
- [Cluster configuration](configuration.md) — all cluster configuration fields.
- [Components](components.md) — installing addon components such as the Secrets Store CSI Driver.
