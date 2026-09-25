---
title: "login"
weight: 30
description: "Authenticate to a cluster via kubectl"
aliases:
  - /docs/cli-reference/login/
---

## Overview

Authenticate to a Podplane cluster and configure kubectl. Podplane opens your browser
for a user login, or uses a short-lived identity token when running as a service or in
a CI build such as GitHub Actions or Buildkite. Tokens are renewed automatically when
needed.

```
podplane login [flags]
```

For service login to work, the cluster's OIDC server must support OIDC
federation. Truster, which `podplane oidc create` and `podplane cluster create`
deploy, includes this support. Its trust policies control the access granted to
specific CI platforms, organizations, repositories, branches, and other claims.

## Options

| Flag | Description |
| --- | --- |
| `-f, --cluster-config string` | Path to a podplane.cluster.jsonc file (default: `./podplane.cluster.jsonc`) |
| `--ca-cert string` | Path, URL, or inline PEM for the Kubernetes API server CA certificate |
| `--callback-port int` | Port for the local OIDC callback HTTP server (default: `8000`) |
| `--headless` | Skip opening a browser; follow the authorize redirect non-interactively |
| `--identity-provider string` | Use `github` or `buildkite`; use `none` to force a user login (default: detect automatically) |
| `--identity-file string` | Read an identity token from a caller-managed file |

## Service login

Set a random keyring password once for the job so each Podplane command can
unlock the same encrypted token store:

```sh
export PODPLANE_KEYRING_PASS="$(openssl rand -hex 32)"
podplane login
podplane push ghcr.io/example/app:latest
podplane deploy web --name app --image ghcr.io/example/app:latest
```

Keep the same value available to each command when these run in separate CI
steps, and do not print it in logs.

GitHub Actions is detected only when `GITHUB_ACTIONS=true` and both Actions ID
token request variables are present. The job needs `permissions: id-token: write`. If auto-detection does not work, specify the provider:

```sh
podplane login -f podplane.cluster.jsonc --identity-provider github
kubectl get nodes
```

Buildkite is detected only when `BUILDKITE=true` and `buildkite-agent` is on
`PATH`. If auto-detection does not work, specify the provider:

```sh
podplane login -f podplane.cluster.jsonc --identity-provider buildkite
kubectl get nodes
```

For a caller-managed workload identity token, keep the file owner-only and
replace it atomically when rotating it:

```sh
chmod 0600 /run/secrets/workload.jwt
podplane login -f podplane.cluster.jsonc --identity-file /run/secrets/workload.jwt
kubectl get nodes
```

Explicit `--identity-provider` or `--identity-file` selection takes precedence
over detection. Use `--identity-provider=none` to force a user login. If both
supported service environments are detected, login exits with an error; select
one explicitly.

## How service login works

GitHub Actions, Buildkite, or the configured identity file supplies a short-lived
identity token. Podplane exchanges it with Truster for a service token and
stores that token in the keyring. Later Podplane and kubectl commands reuse it
until it expires, then acquire and exchange a fresh identity token. The
non-secret auth configuration records the selected identity provider or the
absolute identity-file path so this renewal can happen automatically. In CI,
set `PODPLANE_KEYRING_PASS` to use Podplane's encrypted file-backed keyring
across commands in the same job.
