---
title: "deploy"
weight: 50
description: "Deploy an app using a template"
---

## Overview

Deploys an app to the cluster using a template such as `web` or `worker`. The CLI will prompt to install addon components if the template has required dependencies which are not installed - pass `-y` / `--auto-approve` to skip the prompt.

To update an existing app (e.g. to deploy a new image version), re-run this command with the same `--name`.

```
podplane deploy <template> --name <name> [--image <image>] [flags]
```

Under the hood this resolves the template from the local dependency cache and runs `helm upgrade --install --wait --timeout 2m` by default. 

If the template chart is not cached, the command first runs the same download path used by `podplane deps download`. Helm waits for the rendered Kubernetes resources to become ready before printing the chart notes, so messages such as "Your app is available" are only shown after the workload is ready or the deploy has timed out. Use `--wait=false` to skip readiness waiting or `--timeout` to allow more time.

Deploy uses the selected kubeconfig context to identify the target cluster, then reads the cached cluster summary written by `podplane login -f <podplane.cluster.jsonc>` or `podplane local start`. If that summary is missing, deploy exits and asks you to log in with the cluster config first.

When the cluster config enables a registry mirror, deploy rewrites template image defaults from the templates manifest to use that mirror. User-provided image values take precedence: `--image` overrides `images.app`, and `--set images.<key>=...` overrides that specific template image.

Environment variables can be passed with Docker-style `-e` / `--env` flags. Use `KEY=value` to pass an explicit value, or `KEY` to read the value from the local environment. Repeating the flag sets multiple variables; if the same key is provided more than once, the last value wins.

```bash
podplane deploy web --name hello --image ghcr.io/podplane/hello:latest \
  -e HELLO_MESSAGE="G'Day World!"
```

Environment variable names must use Kubernetes-compatible names such as `HELLO_MESSAGE`. These values are stored in the rendered Deployment and Helm release metadata, so use them for non-secret configuration only.

`--hostname` and `--path` are ergonomic shortcuts for template routing values (`route.hostname` and `route.path`). If the selected template's `values.schema.json` does not support those values, deploy fails before running Helm. Other template-specific values should be configured with Helm-compatible `--set`, for example `--set app.port=8080`. For non-standard external HTTPS ports, set `route.port`, for example `--set route.port=8443`.

Quote values containing Helm list syntax so the shell passes the braces unchanged. For example, the web template accepts a primary app port followed by additional cluster-internal Service ports:

```bash
podplane deploy web --name hello --set 'app.port={8080,8082}'
```

## Options

| Flag | Description |
| --- | --- |
| `--name string` | Name of the app deployment (required) |
| `--image string` | App container image to deploy. If omitted, the template default is used. |
| `-e, --env stringArray` | Set an environment variable on the app container. Use `KEY=value` or `KEY` to read from the local environment. May be specified multiple times. |
| `--hostname string` | External hostname for routing, when supported by the template |
| `--path string` | URL path prefix for routing, when supported by the template |
| `--set stringArray` | Set a template value using Helm `--set` syntax. May be specified multiple times. |
| `-n, --namespace string` | Kubernetes namespace to deploy into; created if missing |
| `--context string` | The name of the kubeconfig context to use (default: current kubeconfig context) |
| `--kubeconfig string` | Path to the kubeconfig file (default: `$KUBECONFIG` or `~/.kube/config`) |
| `--wait bool` | Wait for Kubernetes resources to become ready before printing Helm notes (default: `true`) |
| `--timeout duration` | Time to wait for Kubernetes resources to become ready (default: `2m0s`) |
| `-y, --auto-approve` | Skip confirmation prompts |
