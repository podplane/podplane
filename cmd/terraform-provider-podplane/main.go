// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/podplane/podplane/internal/tfprovider"
)

// main serves the Terraform provider.
func main() {
	if err := providerserver.Serve(context.Background(), tfprovider.NewProvider, providerserver.ServeOpts{
		Address: "registry.terraform.io/podplane/podplane",
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
