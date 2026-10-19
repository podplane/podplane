---
title: "Podplane on Google Cloud"
linkTitle: "Google Cloud"
weight: 20
description: "Google Cloud support in the Podplane runtime"
---

Podplane's underlying runtime supports Google Cloud: Nstance can manage Compute Engine instances, Netsy can store cluster state in Google Cloud Storage, vmconfig can bootstrap Google nodes, and Podplane Secrets can use Google Secret Manager.

Nstance also understands Google preemptible and Spot VM lifecycle signals. When a notice is available, the agent coordinates replacement and draining using the same lifecycle model as other providers.

Provider-specific artifacts can still be prefetched with:

```bash
podplane deps download --providers google
```

## Workload identity

Generated Google Cloud infrastructure creates a Workload Identity Federation pool
and OIDC provider for each cluster. It uploads the Kubernetes ServiceAccount public
JWKS, so Google does not need public access to kube-apiserver's discovery endpoints.
This uses the Kubernetes token issuer, not the OIDC issuer used for human login.

Zot, the Podplane operator, and the GCP Secrets Store CSI provider each authenticate
with their own projected ServiceAccount token. Zot can write the registry bucket;
the node-local registry retains read-only access through the VM identity. No Google
service-account JSON keys are generated. Application secret onboarding remains
Kubernetes-only through `SecretProviderBinding`.

Cilium blocks ordinary Pods from the Google metadata HTTP/HTTPS endpoints on IPv4
and IPv6. DNS on the metadata address and host-service access remain available.
Privileged and host-network platform workloads remain trusted; tenant namespaces
must retain their Pod Security and runtime restrictions.

Signing keys are retained across applies; automatic rotation is not implemented yet.
The active signer and public verification keys are separate so a future rotation
can overlap old and new keys in kube-apiserver and Google's uploaded JWKS.

## Learn more

- [Cloud provider support](./_index.md)
- [Nstance Google Cloud provider](https://github.com/nstance-dev/nstance/blob/main/docs/providers/google-cloud.md)
- [Air-gapped dependency preparation](../air-gapped-clusters.md)
- [Google Secret Manager configuration](../../reference/secrets.md#cluster-operator-responsibilities)
