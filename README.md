# Podplane: Kubernetes Distribution & PaaS

Podplane is an Open Source Kubernetes distribution & PaaS you can deploy in a few minutes to your AWS, Google Cloud, or Proxmox VE environment.

Want an easy-to-use container platform, without vendor lock-in? Use Podplane to:

1. __Save Time__: Deploy everything you need for a container platform in minutes.

2. __Lower Costs__: Use battle tested infrastructure primitives like VMs, not overpriced managed services.

3. __Be In Control__: Podplane is Apache 2.0 licensed, and only uses components with OSI-approved licenses.

The goal is to combine infrastructure and security best practices with an intuitive developer experience, to enable a platform which scales from hobby projects to enterprise production systems.

## How It Works

Using the Podplane CLI, you can deploy a Podplane cluster in a few minutes, in one of two modes:

- __Kubernetes distribution__: minimal cluster so you can BYO your own stack.
    
    - Includes: Core DNS + Cilium CNI. BYO: Ingress controller, CSI drivers, secrets management, everything else!

- __Platform-as-a-Service (PaaS)__: a complete developer platform, ready to deploy your apps.
    
    - Includes: the base distribution, plus cert-manager, Traefik ingress, cloud-specific CSI drivers & snapshots controller, secrets store CSI driver, and more.

Deploying a cluster first generates versionable infrastructure-as-code artifacts such as OpenTofu/Terraform `.tf` files for AWS & Google Cloud, which then deploys a cluster into your public or private cloud of choice.

## Components

Podplane is easy to use and operate because of four unique components which form a new type of Kubernetes-based container platform:

- Cluster state is stored in object storage via [Netsy](https://netsy.dev), not etcd.
- Auto-scaling & provisioning is faster with [Nstance](https://nstance.dev).
- Clusters are pre-imaged with [Prebake](https://prebake.dev) Helm charts.
- OIDC & RBAC is simplified with [Easy OIDC](https://easy-oidc.dev).

Podplane, Netsy, Nstance, Prebake, and Easy OIDC are Open Source projects created by [Nadrama](https://nadrama.com).

## Learn More

Learn more about Podplane at the official project website: [podplane.dev](http://podplane.dev)

## License

Podplane is licensed under the Apache License, Version 2.0.
Copyright 2026 Nadrama Pty Ltd.

See the [LICENSE](./LICENSE) file for details.
