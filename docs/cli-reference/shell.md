---
title: "shell"
weight: 53
description: "Open a shell or run a command in a deployed app"
---

## Overview

Opens a shell in a deployed app, or runs one command when arguments are passed after `--`.

```sh
podplane shell <name> [-- command...] [flags]
```

## Modes

`podplane shell` has four possible modes:

1. **Interactive app shell**: `podplane shell hello` tries `bash -l`, then `sh`, in the selected app container.
2. **One-off command**: `podplane shell hello -- <command...>` runs exactly that command with `kubectl exec`. Debug containers and the shell prompt are not used in this mode.
3. **Ephemeral debug container**: if the app container has no `bash` or `sh`, Podplane can use `kubectl debug` to start a toolbox container in the same pod.
4. **Shell prompt fallback**: if debug is declined, unavailable, or fails, Podplane opens a small client-side prompt that runs each line as a separate `kubectl exec` command.

## Examples

```sh
podplane shell hello
podplane shell hello -c web
podplane shell hello -- npm run migrate
podplane shell hello -- env
```

## Container selection

By default, Podplane chooses the same app container as `podplane logs`: the `kubectl.kubernetes.io/default-container` annotation, then a recognized app container name, then the only container. If multiple containers remain, Podplane prompts in an interactive terminal.

Use `-c, --container` to choose explicitly.

## Ephemeral debug container fallback

For interactive shells, Podplane tries `bash -l`, then `sh`. If neither exists, Podplane may ask whether to start an ephemeral debug container in the same pod. This uses `kubectl debug`, modifies the pod by adding an ephemeral container, and requires RBAC permission for ephemeral containers.

Podplane only offers this fallback when the selected pod is managed by a Deployment, StatefulSet, or DaemonSet, so it can offer a safe restart cleanup afterwards. ReplicaSet-owned pods are resolved back to their owning Deployment. Bare pods, Jobs, and unknown owners skip debug fallback and go straight to the shell prompt.

Podplane labels pods that receive a debug container so they can be filtered:

```sh
kubectl get pods -l podplane.dev/shell-debug=true
```

It also adds one unique annotation per debug container for storing metadata which will not be overwritten by later shell sessions:

```yaml
podplane.dev/shell-debug-<debug-id>: |
  {"container":"<debug-container>","target":"<app-container>","createdAt":"<timestamp>"}
```

After the debug shell exits, Podplane asks whether to restart the owning workload to remove the ephemeral container. Ephemeral containers cannot be removed directly; they disappear when the pod is recreated.

Use `--debug` to skip the debug prompt when no app shell is available, `--no-debug` to skip this fallback, and `--debug-image` to choose the debug image. These flags only apply to interactive shell fallback, not to `-- <command...>` one-off commands.

## Shell prompt fallback

If the debug container is declined or fails, Podplane opens a small shell prompt.

The prompt is client-side: each entered line runs as a new `kubectl exec` command. It always supports `exit`. If the container has an `env` binary, it also supports `export`, `unset`, and local `env` output; otherwise those commands report an error if invoked.

The prompt does not emulate a shell. Pipes, globbing, redirection, aliases, functions, `pwd`, and `cd` require a real shell in the container.

## Options

| Flag | Description |
| --- | --- |
| `-c, --container string` | Container to shell into |
| `--debug` | Start an ephemeral debug container without prompting when no shell is available |
| `--debug-image string` | Image to use for ephemeral debug containers (default: `busybox:latest`) |
| `--no-debug` | Skip ephemeral debug container fallback |
| `-n, --namespace string` | Kubernetes namespace the app was deployed into |
| `--context string` | The name of the kubeconfig context to use (default: current kubeconfig context) |
| `--kubeconfig string` | Path to the kubeconfig file (default: `$KUBECONFIG` or `~/.kube/config`) |
