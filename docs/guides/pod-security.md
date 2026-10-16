---
title: "Pod Security"
linkTitle: "Pod Security"
weight: 75
description: "Podplane's default Pod Security levels, secure defaults, and how to relax them"
---

# Pod Security

Podplane runs Pods with secure defaults, and lets you relax them where a
workload genuinely needs more access. Two built-in Kubernetes mechanisms work
together:

- [Pod Security Admission](https://kubernetes.io/docs/concepts/security/pod-security-admission/)
  enforces a [Pod Security Standard](https://kubernetes.io/docs/concepts/security/pod-security-standards/)
  level per namespace: `privileged`, `baseline`, or `restricted`.
- If you deploy a Pod which has not set security settings, a mutating admission 
  policy will fills in secure defaults, so ordinary Pods meet `restricted` without any
  configuration.

## Defaults

Namespaces without a `pod-security.kubernetes.io/enforce` label, which includes
every namespace you create, enforce `baseline` by default. Pods that do not also meet
`restricted` are allowed, and the API server records them in its audit log.

`baseline` blocks the settings that give a Pod access to its node: hostPath
volumes, privileged containers, host networking, PID and IPC namespaces, host
ports, and added capabilities beyond a small safe set.

In every namespace except `privileged` ones and `kube-system`, Podplane also
fills in these settings with secure defaults when a Pod omits them:

| Setting | Value |
| --- | --- |
| Pod `seccompProfile` | `RuntimeDefault` |
| Container `allowPrivilegeEscalation` | `false` |
| Container `capabilities` | `drop: ["ALL"]` |
| Container `runAsNonRoot` | `true` |

Those settings are never changed if a Pod sets them explicitly. The `web` and `worker`
[deployment templates](../reference/templates.md) set these values by default themselves also.

## Platform Namespaces

Each `platform-*` namespace declares its own Pod Security level. Most are `restricted`. 
These components need host access and run in `privileged` namespaces:

| Component | Why |
| --- | --- |
| `cilium` | Manages node networking: privileged containers, host networking, hostPath mounts, and added capabilities |
| `coredns` | Mounts the node's systemd-resolved `resolv.conf` for upstream DNS |
| `csi-aws-ebs` | Attaches and mounts volumes on the node |
| `secrets-store-csi-driver` | Mounts secrets into Pods on the node |
| `secrets-store-csi-provider-*` | Mount the CSI driver's provider socket directory |
| `envoy-gateway` | Binds host port 443 and, with node certificates, mounts them from the node |
| `node-problem-detector` | Reads node logs, the journal, and the containerd socket |

`kube-system` is exempt from Pod Security, because the VM-managed tunnel static
Pod runs there with host networking.

Only admins can create `kube-*` and `platform-*` namespaces or change namespace
labels, so other groups cannot use these levels. See [RBAC](rbac.md).

A namespace's Pod Security level doesn't choose its Pods' runtime: a
`privileged` namespace still runs Pods in gVisor unless an admin also allows
runc. See [Container Runtimes](container-runtimes.md).

## Relaxing the Defaults

### Images that run as root

This is the most common case. Many public images, and images built `FROM`
language base images without a `USER`, run as root. With the defaults, their
containers fail to start with `container has runAsNonRoot and image will run
as root`.

Prefer a non-root image: add a numeric `USER` to the Dockerfile. If the image
must run as root, opt out for that app:

```sh
podplane deploy web --name nginx --image nginx \
  --set podSecurityContext.runAsNonRoot=false
```

Some root images also need default Linux capabilities, for example to switch
users or change file ownership. Restore them too:

```sh
podplane deploy web --name nginx --image nginx \
  --set podSecurityContext.runAsNonRoot=false \
  --set-json 'containerSecurityContext={"allowPrivilegeEscalation":false,"capabilities":{}}'
```

In your own manifests, set `runAsNonRoot: false` and `capabilities: {}`
explicitly. Unlabelled namespaces allow this; `restricted` namespaces do not.

### Workloads that need host access

Node agents such as monitoring exporters, log or security agents, storage
drivers, and GPU device plugins usually need hostPath volumes, host networking,
or privileged containers. Docker-in-Docker CI builds need privileged
containers. `baseline` rejects these Pods.

An admin can run them in a dedicated namespace labelled `privileged`, which
also disables the secure defaults there:

```sh
kubectl create namespace monitoring
kubectl label namespace monitoring pod-security.kubernetes.io/enforce=privileged
```

Keep such namespaces separate from application namespaces. Anyone who can
create Pods in a `privileged` namespace can gain control of its nodes.

Pods that use host networking or the node's Unix sockets also need runc, which
an admin allows separately. See [Container Runtimes](container-runtimes.md).

### Stricter namespaces

Label a namespace `restricted` to reject Pods that opt out of the defaults:

```sh
kubectl label namespace payments pod-security.kubernetes.io/enforce=restricted
```

### Platform components and cluster-wide defaults

Platform component levels and the secure defaults policy are set in the
`platform-components` HelmRelease values, which Flux reconciles:

```sh
kubectl edit helmrelease platform-components -n platform-components
```

```yaml
spec:
  values:
    platform:
      components:
        apps:
          zot-registry:
            podSecurity: baseline
        values:
          platform-rbac:
            platform:
              rbac:
                admissionPolicies:
                  podSecurityDefaults:
                    enabled: false
```

Disabling `podSecurityDefaults` restores Kubernetes defaults cluster-wide; the
`baseline` level is still enforced.

## Troubleshooting

When `podplane deploy` times out because of Pod Security, it explains which
setting was rejected or which container cannot start, and how to fix it.

For other workloads, check the Deployment and Pods:

```sh
kubectl describe deployment <name>
kubectl get pods -l app.kubernetes.io/instance=<name>
```

A Pod Security rejection appears as a `FailedCreate` event and a
`ReplicaFailure` condition containing `violates PodSecurity`. A root image
shows a `CreateContainerConfigError` container status.
