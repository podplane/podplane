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

## Local Infrastructure Test Targets

| Target | Purpose |
| --- | --- |
| `make demo-cluster-create` | Try the OpenTofu/Terraform plan-and-apply dashboard with disposable local/fake resources. |
| `make demo-cluster-create-fail` | Try the same `demo-cluster-create` demo with an apply failure. |
| `make floci-start` | Start Floci - local AWS API emulation - with mocked EC2 (so no emulated VM containers boot). |
| `make floci-stop` | Stop Floci, preserving its data. |
| `make floci-destroy` | Stop and remove Floci, permanently deleting its persisted data. |

The demo needs OpenTofu or Terraform, takes about 20 seconds, and cleans up on
exit. Use `PODPLANE_TF_CMD=terraform` to select Terraform or `ARGS=-auto-approve`
to skip review. See [TUI controls](reference/cli/terminal-ui.md).

### Floci Setup

Install Podman or Docker with Compose and start its engine. The targets prefer
Podman; use `FLOCI_RUNNER=docker` on both start and stop to override. Floci listens
on `localhost:4566`; stop any existing emulator using that port first.

Add to `~/.aws/config`:

```ini
[profile floci]
region = us-east-1
output = json
endpoint_url = http://localhost:4566
aws_access_key_id = test
aws_secret_access_key = test
s3 =
    addressing_style = path
```

Run `podplane cluster create`, select AWS, and use profile `floci`. 

If you've made changes to the Podplane OpenTofu/Terraform Provider, and want to
test unreleased provider changes, export `PODPLANE_TF_PROVIDER_DIR="$PWD/bin"` and
OpenTofu/Terraform will use the binary created by `make build`.

The pinned Floci image runs offline once cached. Starts reuse the container; after
editing `scripts/compose.floci.yaml`, apply it with
`podman compose -p podplane-floci -f scripts/compose.floci.yaml up -d --force-recreate`
(or `docker compose`). Neither Floci nor the demo tests a working cluster.

## Local Cluster Development Flow

Before the first `local start`, download dependencies using the local `components`, `vmconfig`, and `templates` manifests:

```sh
go run . deps download \
  --components ../components/manifests/components.json \
  --templates ../templates/manifests/templates.json \
  --vmconfig ../vmconfig/manifests/knc.debian-13.arm64.json
  # or for x86 dev machines:
  # --vmconfig ../vmconfig/manifests/knc.debian-13.amd64.json
```

By default this downloads all templates, provider-neutral dependencies, core component images, addon component images, and the components Git source declared by the components manifest. If you are testing provider-specific components or extra addons locally, add filters such as `--providers aws` and `--addons snapshot`.

Then start the local VM. Note that when using local development manifests, start
with `--components none` unless you have also cached the published Podplane seed files
under the local deps server. This creates a bare Kubernetes cluster and avoids
trying to seed `agentic.netsy` from the local dependency cache:

```sh
go run . local start --components none --follow
```

After the VM is running, snapshot the `components` checkout into the local Git
cache and bootstrap components against the local cluster:

```sh
cd ../components
make git-sync
DOMAIN=default.localhost make agentic
```

The components repo provides four local bootstrap paths: `make minimal`,
`make platform`, `make agentic`, and `make all`. Each reads `podplane local status --json` to
discover the local VM HTTPS Git URL and Flux `secretRef` name, and expects
`make git-sync` to have created `~/.podplane/cache/deps/git/components.git` on
the `local-dev` branch. Use `make platform` or `make agentic` when generating the corresponding seed
snapshot, `make minimal` for internal development and testing, and `make all`
when testing additional addons.

In a second terminal, run the vmconfig watch loop from the `vmconfig` repository:

```sh
cd ../vmconfig
make knc-watch
```

## Why This Works

`podplane deps download` normally fetches the published manifests from `https://deps.podplane.dev`. Passing local paths changes that behavior:

- `--components ../components/manifests/components.json` uses the local components manifest, mirrors the selected component images into the local dependency cache, and clones/fetches the manifest-declared components Git source unless `--skip-components-git` is set. It does not cache the published Podplane seed files (`minimal.netsy`, `platform.netsy`, `agentic.netsy`), so pair this workflow with `go run . local start --components none` when you want to bootstrap components manually from your local `components` checkout.
- `--vmconfig ../vmconfig/manifests/knc.debian-13.arm64.json` uses the local vmconfig manifest, which contains a vmconfig dependency stub instead of a released vmconfig tarball. That makes the local VM user-data skip extracting and running a prebuilt vmconfig package. For the OS image and other dependencies in the manifest, these will be mirrored into the local dependency cache, so using the local manifest means you can test out new dependencies as well as new configuration.
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
