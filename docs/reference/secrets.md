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

Use a `SecretProviderBinding` to declare which secrets an application needs. The Podplane operator creates a `SecretProviderClass` with the same name in the same namespace. The application's Pods reference that class to mount the secrets through the CSI driver. Omit `spec.providerName` in `SecretProviderBinding` to follow the Podplane operator's configured cluster default secrets provider on every reconciliation. Set it explicitly to pin a named provider. If the default is missing or the selected provider is unknown, the operator marks the binding `Ready=False` with reason `Invalid`; it does not choose another provider.

Podplane's templates handle this setup when you use `podplane deploy --secret`. For a custom workload:

1. Store the value in your configured provider with `podplane secret create`.
2. Create a `SecretProviderBinding` for the provider and keys the workload needs. The operator generates a `SecretProviderClass` with the same name.
3. Create a dedicated service account with that name and set it explicitly as the Pod's `spec.serviceAccountName`.
4. Add a Secrets Store CSI volume that references the generated `SecretProviderClass`, then mount it read-only into the workload.

### Binary values

Secret create/update commands accept UTF-8 text by default. For raw binary input, pass `--binary` on each write without pre-encoding it. This flag does not configure the binding.

For the binding, on the `SecretProviderBinding` spec, the `binary` field is optional and effectively defaults to `false`. Setting it, even to `false`, is only valid for Vault/OpenBao, selected by `spec.providerName` or the cluster default secrets provider.

| Provider | Mounting binary values |
| --- | --- |
| AWS Secrets Manager / GCP | Original bytes mount automatically; do not set `binary` on your binding. |
| Vault / OpenBao | Stored as base64; set `spec.items[].binary: true` to decode when mounted. |
| AWS SSM Parameter Store | Mounts base64 text; omit `binary` and decode in the application. |

PEM and application-defined base64 strings remain text; do not use `--binary` for them.

### Access controls

By default, a Pod can mount a `SecretProviderClass` only when its name matches the Pod's service account. For example, a Pod running as `api` can mount the `api` class, but not `database`. Pods using Secrets Store CSI volumes must set `spec.serviceAccountName` explicitly.

Applications should use bindings rather than create `SecretProviderClass` objects themselves. Only platform administrators, the Podplane operator, and the platform Flux Helm controller can create or edit those classes directly. An admission policy enforces this even when RBAC would otherwise allow the request.

These rules do not isolate applications within a namespace: anyone who can create workloads there can use its service accounts and bindings. Keep applications that must not share secrets in separate namespaces. Standard tenant roles cannot access Podplane's reserved platform namespaces.

The `platform-rbac` chart installs the admission policies independently of the operator. Removing the operator therefore does not remove these restrictions.

### Namespace exceptions

For custom CSI setups, cluster administrators can relax the name-matching rule in selected namespaces. Exceptions are disabled by default. To allow them, set these values on the `platform-rbac` chart:

```yaml
platform:
  rbac:
    admissionPolicies:
      secretProviderClassRestriction:
        excludeNamespacesWithLabel:
          enabled: true
```

Then add this label to each namespace that needs an exception:

```yaml
metadata:
  labels:
    secrets.podplane.dev/secretproviderclass-restriction-policy: bypass
```

The label has no effect unless exceptions are enabled. In exempt namespaces, Pods can mount classes with names different from their service accounts. Restrictions on creating or editing classes still apply, as does the rule reserving the workload CA signing key for the operator's own class.

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

For Google Cloud clusters, Podplane sets up separate Workload Identity Federation identities for the operator and the GCP CSI provider. You can give new applications access to secrets through `SecretProviderBinding` without changing OpenTofu/Terraform or adding Google IAM grants for each application.

The operator manages secrets and their versions. The CSI provider uses its own credentials (`auth: provider-adc`) to read and mount secret values, including the workload CA key. The operator can read values directly only for its ingress-certificate state.

Google requires project-wide permission to create and list secrets. Permissions to manage versions and read values are limited to the configured cluster prefixes; they do not include Nstance's bootstrap secrets.

Vault/OpenBao providers use Kubernetes/JWT auth, the same mechanism used by the Secrets Store CSI providers for workloads. The operator authenticates with its own Kubernetes service account token using `auth_path` (default `auth/kubernetes`) and `operator_role` (default `podplane-operator`). The workload read path authenticates separately as the workload Pod service account through the generated `SecretProviderClass` `roleName`.

Vault and OpenBao addresses must use HTTPS. `ca_cert` may be used for endpoints served by a private CA. Local Podplane clusters set this for the local fakevault endpoint automatically.

Local fakevault workload logins are read-only and limited to `secret/data/<cluster.id>/<namespace>/<service-account-name>/<key>`. The requested role must match the service account name; the namespace comes from the verified Kubernetes JWT. Workloads cannot list secrets, read the workload CA key, or change secret data. Login tokens expire no later than the Kubernetes JWT or one hour after login. Only the dedicated Podplane operator identity can use the cluster-wide management role; the host's permission-protected Unix socket remains available for local management.

Before creating a managed cluster that uses Vault or OpenBao as its default provider, provision one unencrypted PKCS#8 Ed25519 private key in the KV-v2 field `value` at `<mount_path>/data/<key_prefix>/workload-ca-key`. `mount_path` defaults to `secret`, and `key_prefix` defaults to `cluster.id`. Grant the operator role read-only access to that exact path. Podplane does not require a Vault/OpenBao token outside the cluster.

Cluster admins should grant RBAC to the Podplane aggregated secrets API deliberately. Normal Kubernetes authorization controls who can read key metadata, create new values, overwrite existing values, restore archived values, and permanently destroy provider data.

## Learn More

- [Manage application secrets](../guides/manage-secrets.md) — create a secret and mount it into an application.
- [`podplane secret` CLI reference](cli/commands/secret.md) — command syntax and flags.
- [Cluster configuration](configuration.md) — all cluster configuration fields.
- [Components](components.md) — installing addon components such as the Secrets Store CSI Driver.
