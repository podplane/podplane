---
title: "Secure secrets management with your backend of choice"
linkTitle: "Secrets Management"
weight: 20
description: "Store secrets outside cluster state and mount them safely into applications"
---

Podplane aims to make secrets feel like part of the normal deployment workflow, without putting sensitive values into cluster state (etcd/Netsy/object storage), Git, Helm values/Kubernetes manifests, or shell history.

The CLI encrypts secret values locally before sending them to the Podplane operator. The operator stores them in your configured cluster secrets store backend: AWS Secrets Manager, AWS Parameter Store, Google Secret Manager, Vault, or OpenBao.

```bash
podplane secret create --for hello database-url
podplane deploy web --name hello --secret database-url
```

## Safer workload access

Applications receive secrets as files through the [Secrets Store CSI Driver](https://secrets-store-csi-driver.sigs.k8s.io/). Values are fetched from the external provider when the Pod starts and do not need to be persisted in Kubernetes cluster state.

For older applications that can only consume Kubernetes Secrets or environment variables, Podplane supports the Secrets Store CSI Driver Kubernetes secret sync feature as an explicit opt-in. This is disabled by default because copying a value into a Kubernetes Secret increases risk around who and what can read it.

Kubernetes RBAC controls secret metadata and lifecycle operations separately, including create, update, archive, restore, and permanent destruction. Commands that inspect secrets return metadata only and never print stored values back to the terminal.

## Learn more

- [Secrets guide](../guides/secrets.md) — provider setup, workload bindings, lifecycle behavior, and Kubernetes Secret sync.
- [`podplane secret` reference](../reference/cli/commands/secret.md)
- [Cluster configuration](../reference/configuration.md)
