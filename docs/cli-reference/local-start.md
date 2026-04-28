---
title: "local start"
weight: 70
description: "Start a local cluster VM"
---

## Overview

Starts a local single-node cluster VM, creating it if it doesn't exist. Packages are automatically downloaded and cached if not already present. A local server (serving packages and hosting a fake OIDC server) is started in the background.

If a name is omitted, `default` is used.

```
podplane local start [name] [flags]
```
