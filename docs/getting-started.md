---
title: "Getting Started"
weight: 10
description: "Get started with Podplane quickly."
---

# Getting Started with Podplane

Podplane can deploy clusters on AWS, Google Cloud, or Proxmox environments.

Using the Podplane CLI, you can deploy a Podplane cluster in a few minutes.

Every cluster comes with CoreDNS and Cilium CNI built-in. You are also able to select addon components to install like Traefik ingress controller or CSI drivers, either during cluster creation or later using `podplane install`.

Deploying a cluster first generates versionable infrastructure-as-code artifacts such as OpenTofu/Terraform `.tf` files for AWS & Google Cloud, which then deploys a cluster into your public or private cloud of choice.

## Step 1: Install the Podplane CLI

macOS via [Homebrew](https://brew.sh/):

```bash
brew install podplane/podplane
```

or via [Go](https://go.dev/):

```bash
go install github.com/podplane/podplane@latest
```

## Step 2: Create Cluster

```bash
podplane cluster create
```

Follow the prompts to specify:

- Which cloud/provider to use.
- Provider config such as account/project/profile and region.
- Auth server URL, or opt to deploy a new [Easy OIDC](https://easy-oidc.dev) server.
- Cluster layout e.g. single node, separate control plane/ingress layers, etc.
- Networking configuration e.g. CIDR block for VPC and Subnet(s), provider zone(s).
- Default CPU architecture.
- Cluster name.
- Which components to install (e.g. Traefik, CSI drivers, etc)

This will:

1. Create a `podplane.cluster.jsonc` file in the current directory
2. Generate the relevant infrastructure-as-code artifacts
3. For AWS/Google Cloud:
    1. Confirm if you want to immediately deploy
    2. Deploy using OpenTofu (or Terraform) `apply` command

## Step 3: Login

The Podplane CLI can automatically configure your local `kubeconfig` via `kubectl` using the login command:

```bash
podplane login
```

This will open a browser window (or print a URL to the terminal) to login via your configured OAuth provider. Once successful, you can then use all your favourite tools e.g.

```bash
kubectl get nodes --context cluster-name
```

## Step 4: Deploy Your App

You can use the Podplane CLI to deploy your apps:

```bash
podplane deploy web --name test --image caddy
```

This will print a URL you can use to view the [Caddy server](https://caddyserver.com/) "Your web server is working" default page.

Note: The `deploy` template may require specific addon components to be installed in the cluster. If they aren't installed, the CLI will prompt you to install them e.g. web apps require Traefik ingress controller.
