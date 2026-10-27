---
title: "deps import"
weight: 83
description: "Import dependencies from a copied offline bundle"
---

## Overview

Copy the **whole** `podplane-<platform>` directory matching your computer OS and architecture from a prepared USB drive or network share. Once files are copied, it's safe to eject the drive / disconnect from the network share and proceed offline.

**Intel Macs:** use `podplane-macos-amd64`. QEMU is not included in this bundle; install it before going offline (for example, `brew install qemu`). Both `qemu-img` and `qemu-system-x86_64` must be on `PATH`. Other supported platforms include QEMU.

For macOS and Linux, assuming the directory is in your home directory run this:

```sh
export PODPLANE_BUNDLE="$HOME/podplane-macos-arm64"
export PATH="$PODPLANE_BUNDLE/bin:$PATH"
podplane deps import "$PODPLANE_BUNDLE"
podplane local start --offline
```

If you need to open any new terminals, keep the bundle intact and repeat the PODPLANE_BUNDLE and PATH export before running commands.

The tools run from the copied bundle; only Podplane dependencies are imported into the configured local cache (e.g. `~/.podplane`). QEMU needs its sibling libraries and firmware, so do not move individual executables. If you wish to use pre-installed components such as QEMU, simply remove the executables from the `bin` directory in the bundle.

Import never downloads or executes bundled tools. It checks the format, guest architecture, inventory, file sizes and digests/hashes, then verifies the dependency selection in temporary staging before changing the cache. It installs dependencies and OCI indexes before publishing manifests. Repeating an import is safe; unrelated cached artifacts and other architecture images are retained. Interrupted imports can leave additional cache files and can be retried. Import does not replace credentials, cluster configuration, or VM disks.

Allow space for the copied bundle, a temporary import copy, and the installed dependency cache, plus VM storage. On checksum failure, recopy the full bundle. An incompatible format requires a compatible CLI/bundle. The inventory is an integrity check, not a signature: obtain bundles from a trusted distributor.

`local start --offline` verifies the complete default local dependency selection and disables automatic dependency downloads and background update checks. Missing or corrupt dependencies fail before VM startup. It is not a network sandbox: applications and cloud APIs may still require internet access.

```text
podplane deps import <directory>
```

See [deps bundle](deps-bundle.md) for preparing a drive. `deps download` remains the online refresh command.
