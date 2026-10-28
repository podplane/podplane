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

The command creates independent `podplane-macos-arm64`, `podplane-macos-amd64`, `podplane-linux-arm64`, and `podplane-linux-amd64` directories. Recipients copy one complete directory locally, eject the drive, then follow [deps import](deps-import.md). No Go installation is required.

**Intel Macs require preinstalled QEMU.** The `podplane-macos-amd64` bundle includes the other tools and all selected dependencies, but not QEMU. Before going offline, install QEMU (for example, `brew install qemu`) and ensure `qemu-img` and `qemu-system-x86_64` are on `PATH`. Apple Silicon and Linux bundles include QEMU and do not require Homebrew or a preinstalled QEMU.

Each bundle contains, for example:

```text
podplane-macos-arm64/
  podplane-bundle.json # creation time, platform, versions, selection and contents checksum
  bin/              # podplane, kubectl, helm, mkcert, qemu-img, native qemu-system
  lib/              # QEMU's non-system libraries, where needed
  share/            # QEMU firmware and bundled licenses
  deps/             # Podplane VM artifacts, manifests, Git source, template charts, seeds
  registry/         # Podplane OCI images for the selected architecture
```

Keep the bundle directory intact: adding, removing, renaming or editing files will cause `deps import` to reject it. Permission changes from USB filesystems and macOS bookkeeping files (`.DS_Store`, `._*`) are ignored.

Preparation reuses verified files from your existing dependency cache and downloads missing artifacts there. Host tools come directly from published upstream releases, defaulting to the latest stable releases, and are cached separately under `tools/`. QEMU comes from `jzombie/lab-qemu-portable` on Apple Silicon and `hermeticbuild/qemu-prebuilt` on Linux. The selection matches `deps download`, including agentic addons.

Bundles are assembled directly in a single `bundles/` staging directory in Podplane's XDG-aware cache (`~/.podplane/cache/bundles` on macOS, `$XDG_CACHE_HOME/podplane/bundles` or `~/.cache/podplane/bundles` on Linux), without versioned or hashed subdirectories. Only after preparation succeeds are the assembled directories copied to the destination. Staging is not stored on the thumbdrive.

The staging directory is retained on failure and, by default, after success. Repeat the command to resume an interrupted copy, or change only the destination to write another drive with the same prepared versions. Matching destination files are skipped, incomplete or damaged files are replaced, and `podplane-bundle.json` is published last. A different bundle or an unrelated nonempty directory is not overwritten.

Use `--cleanup` to remove staging after a successful copy; dependency and host-tool downloads remain cached. Use `--refresh` to replace the staged selection or resolve current releases again, reusing cached downloads. Changing platforms, providers, addons, or the CLI release requires `--refresh` when staging is already complete. Copy refreshed bundles to a fresh destination. After a failed `--refresh` run, omit that flag to resume the prepared copy.

On USB thumb drives, use a filesystem such as exFAT that supports large files.

## Options

| Flag | Description |
| --- | --- |
| `--platforms strings` | Comma-separated host platforms; defaults to the four listed above |
| `--version string` | Podplane CLI release tag; defaults to latest stable when preparing new staging |
| `--refresh` | Rebuild staging using current releases, reusing cached downloads |
| `--cleanup` | Remove staging only after all copies succeed; retain download caches |
| `--providers string` | Provider selection as for `deps download`; defaults to `openbao,local` |
| `--addons strings` | Additional addon images, or `all`; agentic addons are always included |
| `-f, --cluster-config string` | Infer providers from a cluster config |

For the standard offline local-cluster flow, keep the default provider selection. Custom provider selections prepare those dependencies, not necessarily everything a local cluster needs. Bundles do not include application-specific images, arbitrary cloud provisioning tools, model weights, or credentials. Hosts need the OS's OpenSSH client (`ssh`); Linux hosts also need KVM access. Windows is not supported at this time.
