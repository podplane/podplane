// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

// Package fakevault exposes a small local-only Vault/OpenBao-compatible API
// for local VMs. It stores encrypted secret files per local cluster, protected
// by one cluster-scoped key in the Podplane/system keyring.
package fakevault
