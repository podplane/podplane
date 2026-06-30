// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package dockerconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

var lookPath = exec.LookPath

// dockerInstalled reports whether the Docker CLI is available on PATH.
func dockerInstalled() bool {
	_, err := lookPath("docker")
	return err == nil
}

// SetCredentialHelper configures Docker to use the podplane credential helper for hostname.
// It returns false with no error when Docker is not installed.
func SetCredentialHelper(hostname string) (bool, error) {
	if hostname == "" {
		return false, nil
	}
	if !dockerInstalled() {
		return false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	dockerDir := filepath.Join(home, ".docker")
	if err := os.MkdirAll(dockerDir, 0o700); err != nil {
		return false, fmt.Errorf("create Docker config directory: %w", err)
	}
	path := filepath.Join(dockerDir, "config.json")
	config := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, &config); err != nil {
			return false, fmt.Errorf("parse Docker config %s: %w", path, err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read Docker config %s: %w", path, err)
	}
	credHelpers, _ := config["credHelpers"].(map[string]any)
	if credHelpers == nil {
		credHelpers = map[string]any{}
		config["credHelpers"] = credHelpers
	}
	if _, ok := credHelpers[hostname]; ok {
		return false, nil
	}
	credHelpers[hostname] = "podplane"
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return false, err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return false, fmt.Errorf("write Docker config %s: %w", path, err)
	}
	return true, nil
}
