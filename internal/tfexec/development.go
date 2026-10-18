// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfexec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// ProviderDirEnvVar explicitly selects a locally built provider, never PATH.
const ProviderDirEnvVar = "PODPLANE_TF_PROVIDER_DIR"

// commandConfig overlays a development provider onto the selected CLI config.
// The shared cache and user configuration are never modified. A private,
// per-invocation file avoids leaking the override into later production runs.
func (c *CLI) commandConfig() (string, func(), error) {
	cleanup := func() {}
	configPath := c.cliConfigFile
	if configPath == "" {
		configPath = os.Getenv("TF_CLI_CONFIG_FILE")
	}
	dir := os.Getenv(ProviderDirEnvVar)
	if dir == "" {
		return configPath, cleanup, nil
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", cleanup, err
	}
	name := "terraform-provider-podplane"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(dir, name)
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		return "", cleanup, fmt.Errorf("%s must contain executable %s; run make build in the Podplane source tree", ProviderDirEnvVar, name)
	}
	if configPath == "" {
		configPath, err = c.defaultConfigPath()
		if err != nil {
			return "", cleanup, err
		}
	}
	var raw []byte
	if configPath != "" {
		raw, err = os.ReadFile(configPath)
		if err != nil {
			return "", cleanup, fmt.Errorf("read Terraform CLI configuration: %w", err)
		}
	}
	raw, err = withProviderOverride(raw, dir)
	if err != nil {
		return "", cleanup, err
	}
	tmp, err := os.CreateTemp("", "podplane-dev-*.tfrc")
	if err != nil {
		return "", cleanup, err
	}
	cleanup = func() { _ = os.Remove(tmp.Name()) }
	_, writeErr := tmp.Write(raw)
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("write development provider configuration: %w", err)
	}
	return tmp.Name(), cleanup, nil
}

// withProviderOverride inserts a final development override before installation
// methods. CLI configuration uses HCL1 (including quoted attribute names), so
// lex it for block boundaries rather than parsing/reformatting it as HCL2.
// Terraform and OpenTofu merge successive dev_overrides blocks, last entry wins.
func withProviderOverride(raw []byte, dir string) ([]byte, error) {
	block := "\n  dev_overrides {\n"
	for _, host := range []string{"registry.terraform.io", "registry.opentofu.org"} {
		block += "    " + strconv.Quote(host+"/podplane/podplane") + " = " + strconv.Quote(filepath.ToSlash(dir)) + "\n"
	}
	block += "  }\n"
	tokens, diagnostics := hclsyntax.LexConfig(raw, "Terraform CLI configuration", hcl.InitialPos)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("read Terraform CLI configuration: %s", diagnostics.Error())
	}
	// Ignore trivia when looking for block headers; leave the source unchanged.
	var significant hclsyntax.Tokens
	for _, token := range tokens {
		if token.Type != hclsyntax.TokenComment && token.Type != hclsyntax.TokenNewline {
			significant = append(significant, token)
		}
	}
	depth, insertion, installations := 0, -1, 0
	inInstallation, override, methods := false, false, false
	for i, token := range significant {
		if token.Type == hclsyntax.TokenIdent && i+1 < len(significant) && significant[i+1].Type == hclsyntax.TokenOBrace {
			if depth == 0 && string(token.Bytes) == "provider_installation" {
				installations++
				inInstallation = true
				insertion = significant[i+1].Range.End.Byte
			} else if inInstallation && depth == 1 {
				override = string(token.Bytes) == "dev_overrides" && !methods
				if !override {
					methods = true
				}
			}
		}
		switch token.Type {
		case hclsyntax.TokenOBrace:
			depth++
		case hclsyntax.TokenCBrace:
			depth--
			if inInstallation && depth == 1 && override {
				insertion = token.Range.End.Byte
				override = false
			}
			if depth == 0 {
				inInstallation = false
			}
		}
	}
	if installations > 1 {
		return nil, fmt.Errorf("multiple provider_installation blocks in Terraform CLI configuration")
	}
	if insertion < 0 {
		return []byte(string(raw) + "\nprovider_installation {" + block + "  direct {}\n}\n"), nil
	}
	return []byte(string(raw[:insertion]) + block + string(raw[insertion:])), nil
}

// defaultConfigPath follows the supported engines' user configuration locations.
func (c *CLI) defaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	names := []string{filepath.Join(home, ".terraformrc")}
	if runtime.GOOS == "windows" {
		names = []string{filepath.Join(os.Getenv("APPDATA"), "terraform.rc")}
	}
	if strings.Contains(filepath.Base(c.binary), "tofu") {
		tofu := filepath.Join(home, ".tofurc")
		if runtime.GOOS == "windows" {
			tofu = filepath.Join(os.Getenv("APPDATA"), "tofu.rc")
		}
		names = append([]string{tofu}, names...)
	}
	for _, name := range names {
		if _, err := os.Stat(name); err == nil {
			return name, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", nil
}
