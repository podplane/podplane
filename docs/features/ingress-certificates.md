---
title: "Automatic wildcard ingress certificates for your domains"
linkTitle: "Ingress Certificates"
weight: 40
description: "Use ACME DNS validation to secure application ingress for your domains"
---

Podplane can obtain and renew a public TLS certificate for each configured cluster domain.

Each bundle covers both the apex name (e.g. `example.com`) and its wildcard (`*.example.com`), so each new application hostnames does not require a new certificate request - e.g. if you deploy `myapp.example.com` to your cluster, it will work using the `*.example.com` wildcard certificate.

Certificates use the [ACME](https://letsencrypt.org/how-it-works/) DNS-01 flow and default to Let's Encrypt. The Podplane Operator manages the certificate lifecycle and configures Envoy Gateway listeners for both the apex and wildcard names.

## Private Keys are not stored in Kubernetes Secrets

Ingress certificate bundles and ACME account state are stored through the configured Podplane secrets backend, such as AWS Secrets Manager, Google Secret Manager, OpenBao, etc.

Envoy data-plane Pods receive the bundle through the Secrets Store CSI Driver and filesystem SDS, rather than through an ordinary Kubernetes TLS Secret.

For NLB clusters, the operator maintains self-signed per-domain fallback certificates, delivered through CSI and SDS, whether or not ACME is configured. ACME replaces those fallbacks with public certificates for supported domains. Clients do not trust the self-signed fallbacks by default.

Local and tunnel clusters instead use node-local Nstance-issued certificates, without ingress CSI mounts or operator-managed issuance. Clients must trust the issuing CA. Tunnel mode rejects ACME configuration.

ACME DNS automation currently supports AWS Route 53, with more coming soon. Support for a secrets backend such as Google Secret Manager, Vault, or OpenBao does not imply support for that provider's DNS service.

## Learn more

- [ACME and domain configuration](../reference/configuration.md)
- [Deployment templates](deployment-templates.md) — route applications through the managed gateway.
- [Let's Encrypt challenge types](https://letsencrypt.org/docs/challenge-types/#dns-01-challenge)
