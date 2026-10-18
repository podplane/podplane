// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

// Command tf-provider-version prints the TF provider version for a CLI release tag.
package main

import (
	"fmt"
	"os"

	"github.com/podplane/podplane/internal/buildvars"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/tf-provider-version <CLI-version>")
		os.Exit(2)
	}
	version, err := buildvars.ProviderVersionFor(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(version)
}
