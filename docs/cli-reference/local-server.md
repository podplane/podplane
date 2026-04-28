---
title: "local server"
weight: 76
description: "Run a local background server for VMs"
---

## Overview

Runs a local background web server that VMs use to access required files and services. This serves the local package cache and hosts a fake OIDC server for local clusters. This is run automatically in the background when `podplane local start` is used, and stopped on `podplane local stop` of the last running VM.

```
podplane local server [flags]
```
