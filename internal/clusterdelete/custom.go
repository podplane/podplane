// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterdelete

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/podplane/podplane/internal/tfexec"
)

// customResources finds state resources not declared by generated root resources
// or modules. Orphaned resources are included; data sources are absent from State.
func customResources(dir string, state tfexec.State) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "podplane.cluster.*.tf"))
	if err != nil {
		return nil, err
	}
	var roots []string
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(string(raw), "# Podplane managed OpenTofu/Terraform file.\n") {
			continue
		}
		file, diagnostics := hclsyntax.ParseConfig(raw, path, hcl.InitialPos)
		if diagnostics.HasErrors() {
			return nil, fmt.Errorf("read generated resource declarations: %s", diagnostics.Error())
		}
		for _, block := range file.Body.(*hclsyntax.Body).Blocks {
			switch {
			case block.Type == "resource" && len(block.Labels) == 2:
				roots = append(roots, block.Labels[0]+"."+block.Labels[1])
			case block.Type == "module" && len(block.Labels) == 1:
				roots = append(roots, "module."+block.Labels[0])
			}
		}
	}
	var custom []string
	for _, resource := range state.Resources {
		owned := false
		for _, root := range roots {
			if resource.Address == root || strings.HasPrefix(resource.Address, root+"[") ||
				strings.HasPrefix(root, "module.") && strings.HasPrefix(resource.Address, root+".") {
				owned = true
				break
			}
		}
		if !owned {
			custom = append(custom, resource.Address)
		}
	}
	sort.Strings(custom)
	return custom, nil
}
