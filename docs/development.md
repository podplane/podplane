---
title: "Development"
weight: 120
description: "Local development workflow for Podplane itself."
---

# Podplane Development

This guide is for folks working on Podplane itself: primarily when working on the [CLI](https://github.com/podplane/podplane), [vmconfig](https://github.com/podplane/vmconfig), [components](https://github.com/podplane/components), and [templates](https://github.com/podplane/templates), with local VMs used for development through the CLI `local` commands.

## Repository Layout

Use the [`podplane/workspace`](https://github.com/podplane/workspace) repository to keep the Podplane repositories checked out next to each other:

```text
workspace/
├── podplane/
│   ├── podplane/    # Podplane CLI
│   ├── vmconfig/    # VM install/configure scripts and dependency manifests
│   ├── components/  # Helm charts and component manifests
│   └── templates/   # App template Helm charts and template manifests
├── netsy-dev/
...
```

The commands below assume you are in the `podplane` CLI repository and that the `../vmconfig`, `../components`, and `../templates` repository checkouts exist.

## Local Cluster Development Flow

Before the first `local start`, download dependencies using the local `components`, `vmconfig`, and `templates` manifests:

```sh
go run . deps download \
  --components ../components/manifests/components.json \
  --templates ../templates/manifests/templates.json \
  --vmconfig ../vmconfig/manifests/vmconfig_knc_debian-13_arm64.json
  # or for x86 dev machines:
  # --vmconfig ../vmconfig/manifests/vmconfig_knc_debian-13_amd64.json
```

By default this downloads all templates, provider-neutral dependencies, core component images, and recommended addon component images. If you are testing provider-specific components or extra addons locally, add filters such as `--providers aws` and `--addons snapshot`.

Then start the local VM. Note that when using local development manifests, start
with `--components none` unless you have also cached the published Podplane seed files
under the local deps server. This creates a bare Kubernetes cluster and avoids
trying to seed `recommended.netsy` from the local dependency cache:

```sh
go run . local start --components none --follow
```

After the VM is running, bootstrap components from the `components` checkout as
needed (for example, `DOMAIN=default.localhost make recommended`) against the
local cluster.

In a second terminal, run the vmconfig watch loop from the `vmconfig` repository:

```sh
cd ../vmconfig
make knc-watch
```

## Why This Works

`podplane deps download` normally fetches the published manifests from `https://deps.podplane.dev`. Passing local paths changes that behavior:

- `--components ../components/manifests/components.json` uses the local components manifest and mirrors the component images it names into the local dependency cache. It does not cache the published Podplane seed files (`recommended.netsy` / `minimal.netsy`), so pair this workflow with `go run . local start --components none` when you want to bootstrap components manually from your local `components` checkout.
- `--vmconfig ../vmconfig/manifests/vmconfig_knc_debian-13_arm64.json` uses the local vmconfig manifest, which contains a vmconfig dependency stub instead of a released vmconfig tarball. That makes the local VM user-data skip extracting and running a prebuilt vmconfig package. For the OS image and other dependencies in the manifest, these will be mirrored into the local dependency cache, so using the local manifest means you can test out new dependencies as well as new configuration.
- `--templates ../templates/manifests/templates.json` uses the local templates manifest. Entries with `type: "chart"` point at unpacked chart directories; the CLI runs `helm package` and writes the result into the local template chart cache used by `podplane deploy`.

Note that the CLI checks the age of the cached manifests, so you need to re-run `deps download` on your development manifests at least once every 7 days to continue using them for new VMs.

With no prebuilt vmconfig package installed, the VM is ready for `make knc-watch`. That target watches the `vmconfig` templates, manifests, scripts, and Makefile; rebuilds the local `knc` tree on change; syncs it into the VM; and runs install/configure/restart as needed. Note that it will only run install once; to test that again, delete the VM with `go run . local stop --rm` and recreate it.

## What You Can Iterate On

This workflow gives you a local single-node control-plane VM that is useful for testing changes across Podplane's core repositories:

1. Iterate on the `components` manifest and test the images mirrored into the local dependency cache.
2. Iterate on the `templates` manifest and app template charts used by `podplane deploy`.
3. Iterate on the `vmconfig` manifest, templates, install scripts, and service configuration.
4. Test Podplane CLI changes for creating, starting, stopping, syncing, shelling into, deleting local VMs, and deploying apps.
5. Use the resulting bare Kubernetes cluster to test component bootstrap and addon installation.

Helpful commands from the CLI repository:

```sh
go run . local status
go run . local shell
go run . local console
go run . local stop
go run . local delete
# or perform stop+delete in a single command:
# go run . local stop --rm
```

Run `deps download` again before creating a fresh local VM whenever you change the local component, vmconfig, or template manifests, or when you need to refresh mirrored component images or packaged template charts.
