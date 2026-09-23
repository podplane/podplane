# Podplane: Kubernetes Distribution & PaaS

Podplane is an Open Source Kubernetes distribution & PaaS you can deploy in a few minutes to your AWS, Google Cloud, or Proxmox VE environment.

Want an easy-to-use container platform, without vendor lock-in? Use Podplane to:

1. __Save Time__: Deploy everything you need for a container platform in minutes.

2. __Lower Costs__: Use battle-tested infrastructure primitives like VMs, not overpriced managed services.

3. __Be In Control__: Podplane is Apache 2.0 licensed, and only uses components with OSI-approved licenses.

The goal is to combine infrastructure and security best practices with an intuitive developer experience, to enable a platform which scales from hobby projects to enterprise production systems.

## How It Works

Using the Podplane CLI, you can deploy a Podplane cluster in a few minutes.

The default `recommended` cluster includes CoreDNS, Cilium CNI, and commonly used addons such as Envoy Gateway and CSI drivers. Choose the `minimal` seed for only core components, or `none` for an unseeded cluster. Addon components can also be installed later with `podplane install`.

Some CLI commands (like `podplane deploy`) require specific components and will guide you to install them if needed.

Deploying a cluster first generates versionable infrastructure-as-code artifacts such as OpenTofu/Terraform `.tf` files for AWS & Google Cloud, which then deploys a cluster into your public or private cloud of choice.

## Components

Podplane is easy to use and operate because of three sibling projects which form a new type of Kubernetes-based container platform:

- Cluster state is stored in object storage via [Netsy](https://netsy.dev), not etcd.
- Auto-scaling & provisioning is faster with [Nstance](https://nstance.dev).
- OIDC & RBAC is simplified with [Truster](https://truster.dev).

## Learn More

Learn more about Podplane at the official project website: [podplane.dev](https://podplane.dev)

## License

Podplane is licensed under the Apache License, Version 2.0.
Copyright The Podplane Authors.

See the [LICENSE](./LICENSE) file for details.
