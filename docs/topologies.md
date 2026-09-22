---
title: "Recommended Infrastructure Topologies"
weight: 35
description: "Reference infrastructure and network topologies for small startups"
---

# Recommended Infrastructure Topologies

This guide describes a practical starting point for organisation infrastructure, with the aim to balance costs with being secure by design, using approaches such as using network isolation.

This should be read as a reference architecture rather than a hard rule: start with the smallest set of clusters that keeps production safe, then add separation when the cost and operational overhead are worth it.

Most teams should think in terms of five environments:

- **Development**: a shared non-production cluster for engineers and agents to do development and testing.
- **Staging**: an optional production-like cluster for pre-production release testing and validation.
- **Production**: the customer-facing cluster running production services.
- **CI/CD**: either hosted runners or a dedicated self-hosted CI/CD platform environment.
- **Observability**: a dedicated cluster for self-hosted logs, metrics, traces, alerts, and incident dashboards, with hosted monitoring for the observability platform/cluster itself.

You'll often hear of Development, Staging, and Production referred to as "stages" or "deployment stages".

## Reference Cluster Topology

```
┌────────────────┐           ┌──────────────────────────────────────────────┐
│ Git Hosting    ├──────────▶│ CI/CD Cluster *                              │
├────────────────┤ triggers  ├──────────────────────────────────────────────┤
│ branches / PRs │           │ Hosted runners or self-hosted CI/CD platform │
└────────▲───────┘           │ Tests branches / PRs, builds images          │
         │ push              └─────────┬──────────────────────────┬─────────┘
         │ branches / PRs              │ deploys merged changes   │ promotes to
┌────────┴────────────┐      ┌─────────▼──────────┐      ┌────────▼───────────┐
│ Development Cluster │      │ Staging Cluster *  │      │ Production Cluster │
├─────────────────────┤      ├────────────────────┤      ├────────────────────┤
│ dev workloads       │      │ release validation │      │ customer workloads │
│ shared services     │      │ prod-like config   │      │ strict access      │
└──────────┬──────────┘      └─────────┬──────────┘      └─────────┬──────────┘
           │ telemetry                 │ telemetry                 │ telemetry
           └─────────────┬─────────────┼──────────────┬────────────┘
                        ┌▼─────────────▼──────────────▼┐
                        │ Observability Cluster *      │
                        ├──────────────────────────────┤
                        │ Self-hosted logs / metrics   │
                        │ traces / dashboards / alerts │
                        └──────────────┬───────────────┘
                                       │ health checks / alerts
                        ┌──────────────▼───────────────┐
                        │ Hosted Observability         │
                        ├──────────────────────────────┤
                        │ Monitors observability only  │
                        │ Alerts if telemetry is down  │
                        └──────────────────────────────┘
```

## Recommended Environments

### Development

Use a development cluster for day-to-day engineering work that needs shared infrastructure. It can host preview apps, internal tools, seed data, and early integration testing. Your coding agents can run here.

Keep development cheap and flexible:

- Allow faster iteration and looser resource limits than production.
- Expect occasional breakage and data resets.
- Avoid granting development workloads broad access to production data or secrets.
- Prefer a shared dev cluster before creating per-engineer/team/org unit clusters, unless isolation or cost controls require them.

Ideally, development environments are automated/scripted/repeatable.

### Staging

Staging is optional. Add it when your release process needs a production-like place to validate changes before they reach customers.

A staging cluster is useful when you need:

- Final release candidate testing.
- Production-like ingress, certificates, DNS, auth, and network policies.
- Migration and rollback practice.
- A safe place to test infrastructure changes before production.

If your team deploys many times per day and has strong automated tests, preview environments, and progressive rollout controls, you may be able to skip staging at first. If you do create staging, keep it close to production in shape, not necessarily in size.

### Production

Production is the cluster that serves customers and should have the strongest isolation, monitoring, and change controls.

For production:

- Use separate cloud accounts, projects, or at least separate credentials from development and other environments.
- Keep access narrower than in non-production environments.
- Treat configuration, infrastructure, and cluster state as versioned artifacts.
- Send telemetry to an observability system outside the production cluster.
- Practice restore, rollback, and incident workflows before you need them.

### CI/CD

CI/CD does not usually deploy to your development environment. Engineers and agents use development to iterate, then push branches and open PRs. CI/CD runs tests for those branches and PRs, builds images, and after merge can deploy to staging. Production deploys should then ideally be promoted from the tested staging candidate.

For teams that prefer PR-based development, the usual flow is:

- Engineers and agents test changes in the development cluster.
- Changes are pushed to a branch and opened as a PR.
- CI/CD runs tests and policy checks for the branch or PR.
- After the PR merges, CI/CD builds images and deploys the merged main branch state to staging.
- Staging validates the exact candidate that may go to production.
- Production deploys are promoted from the staged candidate artifacts, either automatically after checks pass or manually with approval.

For teams that prefer trunk-based development, the flow is usually shorter:

- Engineers and agents keep changes small and merge frequently to your main branch.
- CI/CD runs tests and builds on every main branch change.
- Successful main branch builds deploy to staging or progressively roll out to production.
- Production safety comes from automated tests, feature flags, canaries, rollbacks, and progressive delivery rather than long-lived PR branches.

CI/CD does not always need its own Kubernetes cluster. For many small teams, hosted platforms like GitHub Actions and Buildkite or similar proprietary platforms are a simpler alternative to start with. Many of these typically enable you to host the "runners", which you can use a dedicated CI/CD Podplane cluster for.

Use hosted CI/CD runners when:

- You want to use a system that does not permit self-hosting of its control plane, but supports self-hosting the runners to keep runners in-network.

Consider self-hosted CI/CD when:

- You want to use a system which supports it, such as [Semaphore](https://semaphore.io/) or [Woodpecker](https://woodpecker-ci.org/)

If you self-host CI/CD, keep it separate from production workloads. A compromised build system often has deployment credentials, so treat it as sensitive infrastructure.

### Observability

Observability should not depend on the production cluster it observes. If production is down, overloaded, or unreachable, you still need access to alerts, logs, metrics, traces, and dashboards.

The recommended default is a dedicated observability cluster running your primary observability platform. This keeps telemetry in-region and in-account, which is usually better for security, data gravity, and network egress costs than sending all logs, metrics, and traces to a third-party provider.

Prefer observability platforms that are open source, cost-effective to operate, and can use cloud object storage for durable retention. Good options to evaluate include:

- [Parseable](https://www.parseable.com/) or [OpenObserve](https://openobserve.ai/) for open source, self-hostable observability platforms that store data on object storage.
- [ClickHouse](https://clickhouse.com/) or [VictoriaMetrics](https://victoriametrics.com/) / [VictoriaLogs](https://docs.victoriametrics.com/victorialogs/) for other open source, self-hostable observability platforms.

Always check current licensing and operational requirements before standardising on a platform, but these projects are good starting points for teams that want to keep telemetry self-hosted.

The observability cluster still needs its own observability. For that, use a small hosted observability provider to monitor the health of the self-hosted observability platform and alert when it is unavailable or falling behind. This avoids the dogfooding problem where the monitoring system fails with the same infrastructure it is supposed to diagnose.

If you do not want to operate a self-hosted observability platform, you can skip the two-tier model and send telemetry directly to a managed observability solution instead. This is simpler, but can increase cost and move more operational data outside your account or region.

Good observability separation means:

- Production, staging, development, and CI/CD all send telemetry to the observability cluster.
- A hosted provider monitors the observability cluster and handles alerts for observability outages.
- Alert delivery for major incidents does not require the production or observability cluster to be healthy.
- Dashboards and incident history remain available during cluster incidents.
- Access to observability is managed separately from cluster admin access.

### Identity and OIDC

Clusters need an OIDC issuer for Kubernetes API authentication and authorization / RBAC.

Many organisations will run one OIDC issuer for all cluster environments, including production, staging, development, observability, and CI/CD. That issuer usually belongs in a dedicated identity account/project or in a production-grade account with stricter controls than non-production environments (and often, even stricter than other production environments).

For example, if you are creating a staging cluster and discover that you also need OIDC, avoid casually deploying the an OIDC server into the staging account unless that is a deliberate trust boundary decision.

Podplane can deploy [Truster](https://truster.dev) for you with `podplane oidc create`, but you can also bring your own OIDC service, such as [Dex](https://dexidp.io), a corporate identity gateway, or a managed third-party identity provider. The important requirements are that the issuer lets you control which users can authenticate and lets you control which groups appear in the groups claim consumed by Podplane / Kubernetes RBAC.

See [RBAC](rbac.md) for Podplane's default Kubernetes access groups.

## Practical Starting Points

### Smallest reasonable setup

For an early startup, consider starting with:

- **Development cluster** for shared non-production work.
- **Production cluster** for customer-facing workloads.
- **Hosted CI/CD runners** for builds and deploys.
- **Managed observability** instead of the two-tier model if simplicity matters more than cost and in-account telemetry storage.
- **Separate cloud accounts/projects** for production and non-production infrastructure.

Skip staging until production changes are risky enough to justify another production-like environment.

### More mature setup

As the team and product mature, add:

- **Staging cluster** that mirrors production configuration and release flow.
- **Dedicated observability cluster** for logs, metrics, traces, and dashboards.
- **Hosted observability** for monitoring and alerting on the observability cluster itself.
- **Separate cloud regions** for Disaster Recovery (DR) scenarios

## Rules of Thumb

- Keep production separate from development, even when budgets are tight.
- Prefer a dedicated observability cluster for primary telemetry, with hosted monitoring for that observability cluster.
- Use fully managed observability when you want the simplest operating model and accept the cost and data-location tradeoffs.
- Add staging for release integration confidence: sometimes the state in your development cluster doesn't represent the latest main branch merged state, so staging allows you to test exactly what gets deployed to production before images are promoted there.
- Make non-production look like production where it affects correctness, but scale it down where it only affects cost.
- Use consistent cluster naming, identity, DNS, and deployment conventions across environments.
