---
title: "Persistent Storage"
weight: 45
description: "Persistent volumes, default storage classes, and data retention"
---

# Persistent Storage

Podplane uses a provider-specific storage driver to provision disks for
Kubernetes PersistentVolumeClaims (PVCs). A PVC requests storage for an
application; the driver creates the disk and mounts it on the node running
the application's Pod.

Fresh clusters with storage components enabled have one default StorageClass:

| Environment | Default StorageClass | Storage | On PVC deletion |
| --- | --- | --- | --- |
| AWS | `platform-aws-ebs-default` | Encrypted EBS | Keep the disk (`Retain`) |
| Google Cloud | `platform-gce-pd-default` | Balanced Persistent Disk, ext4 | Keep the disk (`Retain`) |
| Local VM | `platform-openebs-lvm-default` | LVM on the VM's data disk, ext4 | Delete the volume (`Delete`) |

The CLI selects the driver and configures cloud permissions during cluster
creation. These defaults do not migrate existing volumes or change an
existing cluster's StorageClasses. See [Components](components.md) for
component selection and installation.

## Request storage

Omit `storageClassName` to use the cluster's default class:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: app-data
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
```

Reference the claim from a Pod's `volumes` and mount it with `volumeMounts`.
Setting `storageClassName: ""` disables default-class selection; it is not
equivalent to omitting the field.

All three default classes use `WaitForFirstConsumer`: a new claim normally
stays `Pending` until a Pod uses it. Kubernetes then chooses storage compatible
with the Pod's node and zone. The disks are not shared filesystems:
`ReadWriteOnce` permits mounting on one node at a time.

Source Gateway uses the default class and requests one 30Gi `ReadWriteOnce`
volume by default.

## Grow a volume

All default classes allow expansion. Increase the PVC's
`spec.resources.requests.storage` to request more capacity. Volumes cannot
be shrunk. Local volume expansion also requires free space on the VM's data
disk; see below for increasing that disk's capacity.

## Understand what deletion removes

**On AWS and Google Cloud, deleting a PVC keeps its disk.** The default
`Retain` policy protects the data, but the retained disk still needs
administrator-managed recovery or deletion and may continue to incur charges.

**In a local VM, deleting a PVC deletes its LVM volume and data.** Stopping
and restarting the VM preserves the data disk and its volumes. Running
`podplane local stop --rm` deletes the VM and both disks, including every
local persistent volume. This cannot be undone.

None of these defaults provides backups or a VolumeSnapshotClass. Local
storage installs the snapshot API definitions required by its driver, but
does not install a snapshot controller by default.

## Local storage capacity

Local volumes use a separate sparse qcow2 data disk, initially `256G`; the
root disk remains `128G`. Volumes are tied to that VM and are not replicated.
LVM reserves each volume's full requested capacity, so total allocations
cannot exceed the data disk's available capacity.

To grow the data disk, stop the VM and restart it with a larger size:

```sh
podplane local stop
podplane local start --data-disk-size 512G
```

The next boot makes the additional space available to LVM before kubelet
starts. Existing PVCs keep their requested sizes until you expand them.
Omitting `--data-disk-size` preserves an existing disk's capacity; shrinking
is unsupported. See [`local start`](cli/commands/local-start.md) for size
syntax and restrictions.

The data disk is prepared at boot as the `podplane` volume group on
`/dev/disk/by-id/virtio-podplane-data`. If that disk or volume group is
unavailable, local claims cannot be provisioned. The storage driver does not
format another disk as a fallback.

The default class enables discard, allowing freed filesystem blocks to
release host storage. Deleting a PVC returns its allocated space to LVM,
but does not reduce the data disk's configured virtual capacity. Because
the qcow2 file is sparse, its host disk usage can differ from that capacity.

## Cloud identity and Google disk compatibility

Cloud storage controllers use dedicated permissions rather than node
credentials. The AWS controller uses a dedicated kube2iam role. The Google
controller exchanges a projected Kubernetes service-account token through
Workload Identity Federation; its project and startup zone come from cluster
configuration. It cannot reach Google metadata or fall back to node
credentials. The host-networked Google node plugin reads instance metadata
but receives no federated credentials.

The Google default uses `pd-balanced`. Machine families that support only
Hyperdisk require an administrator-provided `hyperdisk-*` StorageClass.
