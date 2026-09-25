---
title: "Podplane on Google Cloud"
linkTitle: "Google Cloud"
weight: 20
description: "Google Cloud support in the Podplane runtime"
---

Podplane's underlying runtime supports Google Cloud: Nstance can manage Compute Engine instances, Netsy can store cluster state in Google Cloud Storage, vmconfig can bootstrap Google nodes, and Podplane Secrets can use Google Secret Manager.

Nstance also understands Google preemptible and Spot VM lifecycle signals. When a notice is available, the agent coordinates replacement and draining using the same lifecycle model as other providers.

## Current provisioning status

Google Cloud is not yet fully implemented for `podplane cluster create` - but it's coming soon. Running Podplane on Google Cloud therefore requires manually integrating the supported runtime pieces or maintaining your own infrastructure code.

Provider-specific artifacts can still be prefetched with:

```bash
podplane deps download --providers google
```

## Learn more

- [Cloud provider support](./_index.md)
- [Nstance Google Cloud provider](https://github.com/nstance-dev/nstance/blob/main/docs/providers/google-cloud.md)
- [Air-gapped dependency preparation](../air-gapped-clusters.md)
- [Google Secret Manager configuration](../../secrets.md#cluster-operator-responsibilities)
