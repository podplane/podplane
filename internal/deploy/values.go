// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"encoding/json"
	"os"
)

// withValuesFile renders a Helm values JSON file to a temporary path, invokes
// fn with that path, and removes the file before returning.
func withValuesFile(image string, env map[string]string, hostname, path string, port int, fn func(valuesPath string) error) error {
	if env == nil {
		env = map[string]string{}
	}
	values := map[string]any{
		"image": image,
		"env":   env,
	}
	if hostname != "" || path != "" || port != 0 {
		route := map[string]any{}
		if hostname != "" {
			route["hostname"] = hostname
		}
		if path != "" {
			route["path"] = path
		}
		if port != 0 {
			route["port"] = port
		}
		values["route"] = route
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "podplane-deploy-values-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(raw, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return fn(f.Name())
}
