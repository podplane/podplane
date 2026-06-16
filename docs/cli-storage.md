---
title: "CLI Storage"
weight: 90
description: "How Podplane classifies CLI-owned files"
---

# CLI Storage

Podplane stores several kinds of files on your computer when it runs, for example to:

- download and cache dependencies such as local VM OS images and VM component packages
- store login metadata while keeping tokens in the OS-native secret store
- track runtime metadata such as VM process IDs

Podplane uses the same storage categories on macOS, Linux, and Windows:

- **config**: long-term config and auth metadata; meaningful to back up.
- **cache**: downloaded or derived files; safe to delete and recreated on demand.
- **data**: durable local VM/local-cluster files; deleting them deletes local clusters.
- **runtime**: ephemeral process metadata; recreated by restarting Podplane processes.

These categories are based on the XDG Base Directory Specification, a convention for separating different kinds of user-owned application files. You do not need to know XDG to use Podplane; the important idea is that each file is grouped by how safe it is to delete and how important it is to preserve.

Cluster and OIDC server configuration files such as `podplane.cluster.jsonc` and `podplane.oidc.jsonc` are not covered here. See the [Config Files & Context](./cli-overview.md#config-files--context) section in the CLI Overview for more information about those files.

## Base Directories

Podplane uses the following default base directories for CLI-owned files:

| Category | Env override | Linux default | macOS default | Windows default |
|---|---|---|---|---|
| Config | `XDG_CONFIG_HOME` | `~/.config/podplane` | `~/.podplane/config` | `%USERPROFILE%\.podplane\config` |
| Cache | `XDG_CACHE_HOME` | `~/.cache/podplane` | `~/.podplane/cache` | `%USERPROFILE%\.podplane\cache` |
| Data | `XDG_DATA_HOME` | `~/.local/share/podplane` | `~/.podplane/data` | `%USERPROFILE%\.podplane\data` |
| Runtime | `XDG_RUNTIME_DIR` | `$XDG_RUNTIME_DIR/podplane`, or `~/.podplane/run` if unset | `~/.podplane/run` | `%USERPROFILE%\.podplane\run` |

The `XDG_*` environment variables can be used on any operating system to move Podplane's base directories away from the defaults. On macOS and Windows, Podplane defaults to keeping CLI-owned files together under a single `.podplane` directory, separated into category subdirectories.

## Category Definitions

- **Config**
  - User- or tool-authored settings/configuration needed to determine behavior.
  - Persistent across reboots and not regenerated automatically.
  - Small and meaningful to back up.

- **Cache**
  - Reconstructable, downloadable, or derived files.
  - Does not contain authoritative user data or durable local-cluster state.
  - Safe to delete at any time, though deletion may cause redownloads or recomputation.

- **Data**
  - Durable application data, especially local VM and local-cluster resources.
  - Deleting it loses local cluster contents or local service data, not merely cached downloads.
  - Partial deletion may corrupt or break local cluster behavior.
  - Persistent across reboots and meaningful to back up if the user cares about local environments; otherwise generally safe to delete.

- **Runtime**
  - Ephemeral process/session coordination files.
  - Only valid while processes or sessions are running.
  - Deleting it while Podplane, QEMU, or the local server is running may break process coordination.
  - Stopping/restarting processes or rebooting the machine should recover from runtime file problems.
  - Safe to delete when the Podplane CLI is not running.

## File Contents & Category Mapping

### Config Files

- **Auth metadata and cluster summaries file**: stores durable metadata needed to resolve auth state and cluster settings such as the configured registry mirror, but not token secrets. It influences CLI/kubectl auth and deploy behavior and is small, structured, persistent configuration.
- **File-backed keyring fallback file/store**: used only when file-backed keyring is enabled. It is encrypted and contains persistent auth secret material, so it is not cache, data, or runtime. Keep it with auth-related config.

### Cache Files

- **Dependencies cache**: dependency files are grouped by dependency domain under the dependencies cache root. VMConfig manifests and artifacts live under `deps/vmconfig/`, component manifests live under `deps/components/`, template manifests and cached chart OCI data live under `deps/templates/`, and seed manifests/snapshots live under `deps/seeds/`.
- **VMConfig dependency manifests**: metadata about all vmconfig dependencies relevant to the specified architecture and desired OS kind/version, stored under `vmconfig/manifests/`. Safe to delete; the Podplane CLI can fetch it again.
- **Downloaded VMConfig dependency artifacts**: downloadable and checksum-verifiable artifacts stored under `vmconfig/artifacts/<artifact-name>/<version>/<file>`. Includes VM base images, tar.gz archives, and deb packages. Safe to delete, though deletion will require a potentially slow re-download before local clusters can start.
- **Local registry cache**: mirrored container image data for the local zot registry is stored under `registry/`. This shared registry store can be populated by dependency downloads and future commands that build or import local images. Safe to delete; images can be downloaded or rebuilt again.
- **Component dependency cache**: component manifests are cached under `deps/components/manifests/`. Component container images are stored in the shared local registry cache.
- **Template cache**: template manifests are cached under `deps/templates/manifests/`, Helm chart data is stored under `deps/templates/charts/`, and template image data used by local mirrors is stored in the shared local registry cache. Safe to delete; it can be recreated by downloading dependencies again.
- **Cached CA certs derived from inline PEM or URL specs**: reconstructable from the original inline PEM or URL input. Safe to delete; the Podplane CLI can resolve/cache them again.

### Data Files

- **Local generated cluster config stash**: part of the durable local cluster environment. It lets later Podplane CLI commands and the kubectl auth hook understand the local cluster. Not user config, cache, or runtime.
- **Rendered cloud-init user-data**: part of the durable local cluster environment served to the VM. It is generated, but it belongs with local VM/cluster data rather than a separate state area.
- **Local fake OIDC signing key**: part of the durable local cluster environment. Deleting it changes local issuer signing identity and can invalidate local auth assumptions/tokens. Not cache or runtime.
- **QEMU VM disk images**: durable local cluster data. Deleting them destroys the local VM contents.
- **Fake S3 object storage**: durable local service/application data visible to the local cluster. Deleting it loses local object-storage state such as the default `<cluster-id>-netsy` and `<cluster-id>-telemetry` buckets. The special `registry` bucket is backed by the component image cache instead, so it can be recreated by downloading dependencies again.

### Runtime Files

- **Local server PID file**: process coordination only. Valid only while the server process exists. Safe to delete when not running.
- **QEMU VM PID files**: process coordination only. Valid only while the VM process exists. Safe to delete when not running.
- **Local server address/port runtime metadata**: describes a currently running process endpoint. Belongs with the local server PID/runtime record.
