---
title: "secret"
weight: 57
description: "Manage application secrets through the Podplane operator"
---

## Overview

Use `podplane secret` to manage secret values for your applications in your Podplane cluster secrets provider.

Using a secure secrets vault solution is recommended rather than putting those values in Helm values, manifests, shell history, or Kubernetes/etcd cluster state.

For the broader system design, provider model, workload mounting flow, and Kubernetes Secret sync behavior, see [Secrets](../secrets.md).

Secret values are encrypted locally by the CLI before they are sent to the
cluster. Podplane stores them in the cluster's configured secrets provider, such
as AWS Secrets Manager, Google Secret Manager, or OpenBao. Commands that list or
return secrets show metadata only; they do not print secret values.

```bash
podplane secret <command> --for <secret-provider-class-name> [flags]
```

`--for` selects the app or workload the secret belongs to. This should match the
SecretProviderClass name used by the workload. Podplane combines that name with
the selected secrets provider:

```text
<provider-name>.<secret-provider-class-name>
```

Most clusters have one default secrets provider, so you usually do not need to
pass `--provider`. Use it only when the cluster has multiple configured secrets
providers and you want a non-default one.

## Examples

Prompt for a value interactively:

```bash
podplane secret create database-url --for web-api
```

Read the value from stdin:

```bash
printf '%s' "$DATABASE_URL" | podplane secret create database-url --for web-api --stdin
```

Read the exact value bytes from a file:

```bash
podplane secret update tls-key --for ingress-worker --file ./tls.key
```

List keys without showing their values:

```bash
podplane secret list --for web-api
```

Archive, restore, or permanently destroy a key:

```bash
podplane secret delete database-url --for web-api
podplane secret restore database-url --for web-api
podplane secret destroy database-url --for web-api
```

## Commands

| Command | Description |
| --- | --- |
| `create KEY` | Create a missing active key. Fails if the key already exists or is archived. |
| `update KEY` | Overwrite an existing active key. |
| `list` | List keys and status without showing values. |
| `delete KEY` | Archive a key when the provider supports recoverable delete. |
| `delete --all` | Archive all keys for the selected app/workload boundary. |
| `delete KEY --destroy` | Permanently destroy a key instead of archiving it. |
| `restore KEY` | Restore an archived key. |
| `destroy KEY` | Permanently destroy a key. Equivalent to `delete KEY --destroy`. |

## Common Options

| Flag | Description |
| --- | --- |
| `--for string` | App/workload SecretProviderClass name that scopes the secret. Required. |
| `--provider string` | Secrets provider name. Defaults to the cluster default secrets provider. |
| `-n, --namespace string` | Kubernetes namespace. Defaults to the current kubeconfig namespace, or `default`. |
| `--context string` | The kubeconfig context to use. |
| `--kubeconfig string` | Path to the kubeconfig file. |

## Create and Update Options

| Flag | Description |
| --- | --- |
| `--stdin` | Read the secret value from stdin. |
| `--file string` | Read the exact secret value bytes from a file. Mutually exclusive with `--stdin`. |
| `--dry-run client` | Print the encrypted request without writing it. |
| `-o, --output string` | Output format for dry-run or write responses: `json` or `yaml`. |

When neither `--stdin` nor `--file` is set, the CLI prompts for the value on the
terminal without echoing input.

## List Options

| Flag | Description |
| --- | --- |
| `--hide-archived` | Hide archived/restorable keys. |
| `-o, --output string` | Output format: `json` or `yaml`. |

## Delete and Destroy Options

| Flag | Description |
| --- | --- |
| `--all` | Delete every key for the selected app/workload boundary. Only supported by `delete`. |
| `--destroy` | Permanently destroy instead of archive. Only supported by `delete`. |
| `-y, --auto-approve` | Skip the permanent destroy confirmation prompt. |

## Configuration

Secret commands use the secrets providers configured for the cluster. In
`podplane.cluster.jsonc`, that configuration lives under `cluster.secrets`:

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

Only provider names and non-secret provider-selection metadata belong in the
cluster config. Credentials are configured separately by the operator deployment.
