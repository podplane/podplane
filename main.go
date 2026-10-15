// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/podplane/podplane/internal/cmd"
	"github.com/podplane/podplane/internal/config"
)

// main initializes CLI configuration and dispatches commands, including Docker credential helper calls.
func main() {
	c, err := config.Init()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error initializing config:", err)
		os.Exit(1)
	}
	root := cmd.NewRootCmd(c)
	if filepath.Base(os.Args[0]) == "docker-credential-podplane" {
		// This lets users symlink the podplane executable to
		// docker-credential-podplane instead of shipping a wrapper
		// or second binary for Docker registry credential helper support.
		root.SetArgs(append([]string{"hooks", "docker-credentials"}, os.Args[1:]...))
	}
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
