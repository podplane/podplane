---
title: "Container Runtimes"
linkTitle: "Container Runtimes"
weight: 76
description: "How Podplane sandboxes Pods with gVisor, and when and how to use runc instead"
---

# Container Runtimes

Podplane runs Pods in the [gVisor](https://gvisor.dev/) sandbox by default.
gVisor gives each Pod its own application kernel, so a compromised container
attacks gVisor rather than the node's Linux kernel. You don't need to change
your app to use it.

Some workloads need the node's kernel directly, for compatibility or measured
performance. Those can run under `runc`, the standard container runtime, in
namespaces an admin has allowed.

## How Pods get their runtime

Podplane provides two RuntimeClasses, `gvisor` and `runc`, and checks every new
Pod, whoever creates it:

| Pod's `runtimeClassName` | Namespace allows runc | Result |
| --- | --- | --- |
| Not set, or `gvisor` | Either | Runs in gVisor |
| `runc` | No | Rejected |
| `runc` | Yes | Runs under runc |
| Anything else, including `""` | Either | Rejected |

Allowing runc doesn't change the default: Pods in that namespace still run in
gVisor unless they ask for runc. Pod Security still applies under both
runtimes, and runc does not grant privileged access to the node.

## Using runc

To run a [deployment template](../reference/templates.md) under runc:

```sh
podplane deploy web --name app --image myorg/app --set runtimeClassName=runc
```

In your own manifests, set it in the Pod template:

```yaml
spec:
  template:
    spec:
      runtimeClassName: runc
```

The selection applies to every container in the Pod, including init and
ephemeral containers.

### Allowing runc in a namespace

Only cluster admins can allow runc, by labelling the namespace:

```sh
kubectl label namespace native-workloads runtime.podplane.dev/runc=allowed
```

The value must be exactly `allowed`. Remove the label to stop new runc Pods;
running Pods keep their runtime until they are replaced.

The permission applies to everyone who can create Pods in the namespace. To
limit it to particular people or automation, use a dedicated
[restricted](rbac.md) namespace and grant access with RoleBindings:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: native-workloads
  labels:
    runtime.podplane.dev/runc: allowed
    rbac.podplane.dev/restricted: "true"
```

## What differs under gVisor

Most apps run unchanged. These differences come up most often:

- **Watching files:** apps notice updated ConfigMaps, Secrets, and certificates
  by reading them, but inotify watches never fire for those updates. Poll the
  files instead. The `web` template's Envoy sidecar already handles this for automatically reloading rotated certificates.
- **Sockets shared between containers:** containers in a Pod see separate
  copies of an `emptyDir`, so a Unix socket one creates is invisible to the
  others. Share the volume with these Pod annotations, using a mount path that
  doesn't end in `/`:

  ```yaml
  metadata:
    annotations:
      dev.gvisor.spec.mount.<volume>.share: pod
      dev.gvisor.spec.mount.<volume>.type: tmpfs
  ```

- **Node access:** host networking, the node's Unix sockets, `/dev/kmsg`, and
  mount propagation don't work. Host ports and read-only hostPath files do.
- **Seccomp:** a Pod's seccomp profile isn't applied inside the sandbox; gVisor
  itself runs under a strict seccomp filter on the node.

If an app doesn't work in gVisor, it fails visibly. Podplane never falls back
to runc automatically.

## Platform components

Platform components run in gVisor unless they need node access it can't
provide. Cilium, the CSI drivers, the Secrets Store providers, and Node Problem
Detector run under runc, and their namespaces allow it. Kubelet static Pods,
such as the VM tunnel in `kube-system`, also run under runc.

## Bare clusters

Clusters created with `--components=none` have both runtimes on their nodes
but no Podplane runtime policies, so their Pods run under runc. The API
server's Pod Security defaults still apply. Installing components enables the
runtime policies for new Pods; recreate any existing Pods to move them into
gVisor.

## Troubleshooting

See which runtime each Pod uses, and which namespaces allow runc:

```sh
kubectl get pods -A -o custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,RUNTIME:.spec.runtimeClassName
kubectl get namespaces -l runtime.podplane.dev/runc=allowed
```

Rejected Pods appear as a `FailedCreate` event and a `ReplicaFailure` condition
on the Deployment:

```sh
kubectl describe deployment <name>
```

- `RuntimeClass "runc" requires namespace ... to have the label ...`: ask an
  admin to allow runc in the namespace, or remove `runtimeClassName: runc`.
- `RuntimeClass "..." is not supported`: use `gvisor` or `runc`, or omit
  `runtimeClassName`.
