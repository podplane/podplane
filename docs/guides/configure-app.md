---
title: "Configure your app"
linkTitle: "Configure Your App"
weight: 30
description: "Configure deployments, environment variables, secrets, routes, and logs"
---

Podplane deployment templates handle the Kubernetes boilerplate while keeping common application settings available as CLI flags.

## Environment variables

Use `-e` or `--env` for non-secret configuration:

```bash
podplane deploy web --name api \
  -e LOG_LEVEL=info \
  -e API_ORIGIN=https://api.example.com
```

These values are stored in the rendered Deployment and Helm release metadata. Use [Podplane Secrets](secrets.md) for passwords, tokens, private keys, and other sensitive values.

## Routes and ports

The `web` template accepts a hostname and path directly:

```bash
podplane deploy web --name api \
  --hostname api.example.com \
  --path /v1
```

Use schema-backed template values for less common settings:

```bash
podplane deploy web --name api --set app.port=3000
```

See [Deployment Templates](../reference/templates.md) for the complete `web` and `worker` behavior, workload certificates, and supported values.

## Inspect a deployment

```bash
podplane logs api
podplane shell api
```

Re-run `podplane deploy` with the same name to update an application. Remove it with `podplane remove --name api`.

For exact flags, see the [`deploy`](../reference/cli/commands/deploy.md), [`logs`](../reference/cli/commands/logs.md), [`shell`](../reference/cli/commands/shell.md), and [`remove`](../reference/cli/commands/remove.md) references.
