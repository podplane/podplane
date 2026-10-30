---
title: "deps bundle"
weight: 82
description: "Prepare portable offline installation bundles"
---

## Overview

On an internet-connected machine, prepare a USB drive or network share directory for offline installations:

```sh
podplane deps bundle /path/to/thumbdrive
```

The command writes independent `podplane-macos-arm64.tar.gz`, `podplane-macos-amd64.tar.gz`, `podplane-linux-arm64.tar.gz`, and `podplane-linux-amd64.tar.gz` archives. Recipients copy one archive locally (after which they can eject the drive/disconnection the network drive), extract it with the system `tar` command, then follow the [deps import](deps-import.md) instructions. No Go installation or extra decompression tool is required.

**Intel Macs require preinstalled QEMU.** The `podplane-macos-amd64` bundle includes the other tools and all selected dependencies, but not QEMU. Before going offline, install QEMU (for example, `brew install qemu`) and ensure `qemu-img` and `qemu-system-x86_64` are on `PATH`. Apple Silicon and Linux bundles include QEMU and do not require Homebrew or a preinstalled QEMU.

Each archive extracts to one directory containing, for example:

```text
podplane-macos-arm64/
  podplane-bundle.json # creation time, platform, versions, selection and contents checksum
  bin/              # podplane, kubectl, helm, mkcert, qemu-img, native qemu-system
  lib/              # QEMU's non-system libraries, where needed
  share/            # QEMU firmware and bundled licenses
  deps/             # Podplane VM artifacts, manifests, Git source, template charts, seeds
  registry/         # Podplane OCI images for the selected architecture
```

Keep the extracted bundle directory intact: adding, removing, renaming or editing files will cause `deps import` to reject it. Permission changes from USB filesystems and macOS bookkeeping files (`.DS_Store`, `._*`) are ignored.

Preparation reuses verified files from your existing dependency cache and downloads missing artifacts there. Host tools come directly from published upstream releases, defaulting to the latest stable releases, and are cached separately under `tools/`. QEMU comes from `jzombie/lab-qemu-portable` on Apple Silicon and `hermeticbuild/qemu-prebuilt` on Linux. The selection matches `deps download`, including agentic addons.

Bundles are assembled directly in a single `bundles/` staging directory in Podplane's XDG-aware cache (`~/.podplane/cache/bundles` on macOS, `$XDG_CACHE_HOME/podplane/bundles` or `~/.cache/podplane/bundles` on Linux), without versioned or hashed subdirectories. Only after preparation succeeds are the assembled directories compressed into the destination. Staging is not stored on the thumbdrive.

The staging directory is retained on failure and, by default, after success. Repeat the command to resume an interrupted run, or change only the destination to write another drive with the same prepared versions. Each archive is streamed directly to its final name on the destination, so the destination is written only once per platform. An interrupted or failed archive is removed. If any archive already exists, the command asks whether to delete them before starting, and cancels otherwise; it never overwrites an existing archive.

Use `--cleanup` to remove staging after all archives are written; dependency and host-tool downloads remain cached. Use `--refresh` to replace the staged selection or resolve current releases again, reusing cached downloads. Changing platforms, providers, addons, or the CLI release requires `--refresh` when staging is already complete. Write refreshed bundles to a fresh destination. After a failed `--refresh` run, omit that flag to resume writing the prepared archives.

On USB thumb drives, use a filesystem such as exFAT that supports large files.

## Options

| Flag | Description |
| --- | --- |
| `--platforms strings` | Comma-separated host platforms; defaults to the four listed above |
| `--version string` | Podplane CLI release tag; defaults to latest stable when preparing new staging |
| `--refresh` | Rebuild staging using current releases, reusing cached downloads |
| `--cleanup` | Remove staging only after all archives are written; retain download caches |
| `--providers string` | Provider selection as for `deps download`; defaults to `openbao,local` |
| `--addons strings` | Additional addon images, or `all`; agentic addons are always included |
| `-f, --cluster-config string` | Infer providers from a cluster config |

For the standard offline local-cluster flow, keep the default provider selection. Custom provider selections prepare those dependencies, not necessarily everything a local cluster needs. Bundles do not include application-specific images, arbitrary cloud provisioning tools, model weights, or credentials. Hosts need the OS's OpenSSH client (`ssh`); Linux hosts also need KVM access. Windows is not supported at this time.
