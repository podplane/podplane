---
title: "Secure serving and SPIFFE certificates for Pods"
linkTitle: "Workload Certificates"
weight: 30
description: "Give workloads short-lived server certificates and SPIFFE SVID client certificate without Kubernetes TLS Secrets"
---

Podplane can issue short-lived X.509 certificates directly to Pods for two common jobs:

- **Server certificates** prove the DNS identity of a Kubernetes Service the Pod is associated with.
- **Client certificates** identify a workload by namespace and ServiceAccount for application-managed mTLS.

Private keys and certificate chains are projected into the Pod by kubelet using the Kubernetes [Pod Certificates API](https://kubernetes.io/docs/concepts/storage/projected-volumes/#podcertificate-projected-volumes), stable since Kubernetes v1.37.

The Kubernetes `kubelet` handles private keys on the node, automatically issues and renews certificates before they expire, and the private keys/credentials are never stored in a Kubernetes Secret.

The Podplane operator validates each certificate request and signs it with the cluster workload CA.

Podplane clusters use Kubernetes 1.37 or later, where Pod Certificates and ClusterTrustBundles are stable and enabled by default.

## Subject Alternative Names (SANs)

Service certificates are limited to the canonical DNS names of a real Service that selects the Pod.

SPIFFE identities use the form:

```text
spiffe://<trust-domain>/ns/<namespace>/sa/<service-account>
```

## Certificate Authority (CA) & Trust

The operator publishes the workload CA certificate as a Kubernetes [ClusterTrustBundle](https://kubernetes.io/docs/reference/kubernetes-api/certificates/cluster-trust-bundle-v1).

Pods can project that bundle alongside their identity and receive updates automatically.

Trusting the workload CA proves that a certificate belongs to the cluster; applications must still authorize the peer's SPIFFE identity. 

## Learn more

- [Deployment templates](../reference/templates.md#web) — serving certificates and optional SPIFFE identities in the `web` and `worker` templates.
- [SPIFFE identity](https://spiffe.io/docs/latest/spiffe-about/spiffe-concepts/)
- [Kubernetes Pod Certificates and ClusterTrustBundles](https://kubernetes.io/blog/2026/08/28/kubernetes-v1-37-pod-certificates-and-cluster-trust-bundles/)
