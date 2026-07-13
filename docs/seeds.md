---
title: "Seeds"
weight: 55
description: "How Podplane seed files initialize Netsy cluster state"
---

# Seeds

Podplane seed files are `.netsy` files used as a template for initial cluster state.

When you create a cluster, you can choose a seed option of Recommended, Minimal, or None. If you select None, no seed is used and you get bare Kubernetes cluster state. Otherwise, a corresponding seed file (`recommended.netsy` or `minimal.netsy`) has the necessary configuration interpolated into it and a Netsy `bootstrap.netsy` snapshot file is produced for Netsy to import on first startup. Existing Netsy state is never overwritten.

## Seed Files and Snapshots

Netsy stores Kubernetes state as `.netsy` data files in object storage. A snapshot file is a complete set of records up to a revision; chunk files contain newer records. When a control plane VM starts for a new cluster, Netsy restores the latest snapshot plus any newer chunk files before kube-apiserver begins serving.

A Podplane seed file is an input and can be thought of as a snapshot template. Podplane reads it, interpolates cluster-specific settings, and writes `bootstrap.netsy`, which Netsy imports during the first cluster VM startup.

The selected seed is recorded in `cluster.seed` in the cluster configuration file:

```jsonc
{
  "cluster": {
    "seed": {
      "name": "recommended", // recommended or minimal
      "version": "v1.2.3-1",
      "digest": "sha512:..."
    }
  }
}
```

- `recommended` seeds Core Components plus commonly used addon components such as Traefik.
- `minimal` seeds only the Core Components needed for a usable cluster.
- An empty `"seed": {}` object represents None, skips seeding entirely, for a bare cluster you must bootstrap manually.

## How Cluster Creation Uses Seeds

The [Podplane OpenTofu/Terraform provider](https://github.com/podplane/terraform-provider-podplane) uses Podplane's public `pkg/netsyseed` package in-process. The provider:

1. reads the cluster config file
2. resolves the selected seed file through the seeds manifest to determine the seed file version and download the file
3. interpolates cluster-specific platform component values into the seed file
4. writes the output as `bootstrap.netsy`
5. uploads it to S3 or GCS with create-only preconditions

The upload step is deliberately conservative: it checks for existing contents under the configured prefix and uses S3 `If-None-Match: *` or GCS `DoesNotExist`. This preserves the hard safety property that Podplane can seed initial Netsy state only when remote state does not already exist.

`podplane local start` uses the same `pkg/netsyseed` package to initialize local cluster state on first boot. Later starts do not overwrite existing local Netsy state.

## Seeds Repository and Manifest

Published Podplane seed files live in the [podplane/seeds](https://github.com/podplane/seeds) repository. A seeds release publishes:

- a seeds manifest consumed by [podplane deps download](./cli-reference/deps-download.md) and the Terraform provider, referencing:
- seed snapshot files such as `recommended.netsy` and `minimal.netsy` stored in the git repository for the given tag.

The manifest records the available seed version and the seed files' locations, sizes, and digests. Generated cluster configs pin `cluster.seed.version` and `cluster.seed.digest`, and consumers verify the exact seed file before generating the Netsy bootstrap snapshot.

## Podplane Seed Generator

[seedgen](https://github.com/podplane/seedgen) is the tool used to produce seed files for the `podplane/seeds` repository. It reads the Netsy state directly from a Podplane local VM's fake S3 bucket, and produces a `.netsy` seed file containing a filtered and flattened set of Kubernetes resources.

The generated seed file is not yet cluster-specific. Cluster-specific values, such as domain, ACME, network, component source, and platform component values, are applied later by `pkg/netsyseed` during cluster creation.

## Custom Seed Files

The provider resources accept `seed_path` for advanced workflows. `seed_path` may point to a local file or HTTP(S) URL for a custom Podplane seed file. When omitted, Podplane resolves the published seed selected by `cluster.seed`.

Custom seed files should follow the same structure as published seeds: they must contain the platform-components Flux `HelmRelease` and bootstrap `podplane-components` `GitRepository` records that `pkg/netsyseed` updates during interpolation.
