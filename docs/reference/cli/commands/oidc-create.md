---
title: "oidc create"
weight: 20
description: "Generate OIDC configuration and deploy infrastructure"
aliases:
  - /docs/cli-reference/oidc-create/
---

## Overview

Generates or reads a Truster OIDC config file, generates infrastructure-as-code files, and deploys the server to AWS or Google Cloud via OpenTofu/Terraform. When creating a new config interactively, the command asks which cloud provider to use; when run from `podplane cluster create`, it uses the cluster's cloud provider. Managed DNS records use Route 53 on AWS and Cloud DNS on Google Cloud.

The interactive wizard covers the common case: one Google or GitHub login connector. When run from `podplane cluster create`, it registers the cluster's resolved OIDC client ID (the cluster ID unless overridden). Run on its own, it asks for the first cluster ID and registers that client. The default CLI redirect URI is `http://localhost:8000/callback`. For each additional cluster, add its client ID under `oidc.truster.static_policy.clients` and run the command again. The config's `oidc.truster` section is passed to Truster unchanged, so to add email login, trust policies, or any other Truster setting, edit that section and run the command again. See [Truster configuration](../../configuration.md#truster-configuration) for the settings Podplane manages itself.

A new Truster server created from the `cluster create` wizard reuses the cluster's AWS or Google Cloud details without asking again. Suggested secret references use that account or project too. Hostname, DNS, login, and secret questions remain specific to the OIDC server. Standalone `oidc create` still collects cloud settings.

The wizard also asks for your verified Google or GitHub sign-in email. It saves this address in `static_policy.user_group_mappings.initial-admin`, granting only that user the `podplane:admins` group for the first client's `user_group_mapping`. Unmapped users remain denied. Podplane's platform RBAC binds this group to Kubernetes cluster-admin; bare or minimal clusters need an equivalent binding before this identity can administer them. This email is an access-control setting, not a mailing-list signup. Additional clients need an explicit user-group mapping too; adding an empty client does not grant access.

On both AWS and Google Cloud, the Truster module uses the same hostname-derived name prefix with a hash suffix. Google uses it as the network name; AWS uses it in network `Name` tags with `-vpc`, `-igw`, and `-rt` suffixes. The shared prefix fits Google's stricter 30-character service-account ID limit and distinguishes deployments with different OIDC hostnames.

OpenTofu/Terraform state is stored remotely in a customer-owned object storage bucket named `<hostname-slug>-<account-id>-tfstate-<hash>` on AWS or `<hostname-slug>-<project-id>-tfstate-<hash>` on Google Cloud. The hostname is lowercased and its trailing dot removed. The slug replaces dots with hyphens and is truncated to keep the bucket name within 63 characters; the hash is the first 16 hexadecimal characters of SHA-256 of the normalized hostname, distinguishing names such as `auth.example.com` and `auth-example.com`.

Before OpenTofu/Terraform initializes, Podplane checks that bucket and, if it is absent, shows its name and asks before creating it. The bucket follows the same policy as cluster state buckets: private, encrypted, versioned, with state locking, and keeping noncurrent state versions for 90 days. Podplane generates the non-secret `podplane.oidc.backend.hcl` alongside the `.tf` files; retain it with the stack so a fresh checkout can use the remote state. `--no-apply` writes that configuration but does not contact object storage or create a bucket.

OIDC buckets also carry ownership markers, checked before apply and destroy:

- AWS tags: `podplane:stack=oidc` and `podplane:hostname=<normalized hostname>`.
- Google labels: `podplane-stack=oidc` and `podplane-hostname=<SHA-256 of normalized hostname encoded as lowercase unpadded base32>` (a hash is used because Google labels cannot hold arbitrary domain names).

Mismatched or missing markers stop the command, including with `--auto-approve`. To explicitly adopt an unmarked bucket, first verify that its state belongs to this OIDC hostname (or that it is empty), then set the exact tags/labels printed in the error. Do not overwrite another server's ownership markers.

The hostname identifies the deployment's state bucket. Changing it is not an in-place server rename.

```
podplane oidc create [flags]
```

Before generating Terraform for an apply, the command performs the same read-only remote-state access check as [`cluster create`](cluster-create.md). It stops early on authentication, permission, or existing-bucket safety failures, but allows a missing bucket to be created later after confirmation. `--no-apply` skips the check. Google application-credential reauthentication failures include instructions for refreshing the credentials.

OIDC creation uses the shared [OpenTofu/Terraform plan-and-apply dashboard](../terminal-ui.md#opentofuterraform-plan-and-apply-dashboard)
for plan review, resource details, logs, and apply progress. When invoked from the cluster
wizard, OIDC creation completes its own review/apply flow before cluster creation resumes.

## Options

| Flag | Description |
| --- | --- |
| `-f, --oidc-config string` | Path to the OIDC config file (default: `podplane.oidc.jsonc` in the current directory) |
| `--no-apply` | Generate OpenTofu/Terraform files but do not run apply |
| `-y, --auto-approve` | Skip confirmation prompts and approve the saved infrastructure plan |
