// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package appbuild

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var imageNamePartRE = regexp.MustCompile(`[^a-z0-9._-]+`)

// DetectAppName returns a best-effort app name for a build context.
func DetectAppName(dir string) (string, error) {
	if name, err := packageJSONName(filepath.Join(dir, "package.json")); err != nil {
		return "", err
	} else if name != "" {
		return normalizeAppName(name), nil
	}
	if name, err := goModuleName(filepath.Join(dir, "go.mod")); err != nil {
		return "", err
	} else if name != "" {
		return normalizeAppName(name), nil
	}
	if name, err := cargoPackageName(filepath.Join(dir, "Cargo.toml")); err != nil {
		return "", err
	} else if name != "" {
		return normalizeAppName(name), nil
	}
	if name, err := csharpProjectName(dir); err != nil {
		return "", err
	} else if name != "" {
		return normalizeAppName(name), nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve build context: %w", err)
	}
	return normalizeAppName(filepath.Base(abs)), nil
}

// normalizeAppName converts project metadata into a registry-safe app name.
func normalizeAppName(name string) string {
	name = strings.TrimSpace(strings.TrimPrefix(name, "@"))
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ToLower(name)
	name = imageNamePartRE.ReplaceAllString(name, "-")
	name = strings.Trim(name, ".-_")
	if name == "" {
		return "app"
	}
	return name
}

// packageJSONName returns the package name from package.json when present.
func packageJSONName(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read package.json: %w", err)
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &pkg); err != nil {
		return "", fmt.Errorf("parse package.json: %w", err)
	}
	return pkg.Name, nil
}

// goModuleName returns the final module path component from go.mod when present.
func goModuleName(filename string) (string, error) {
	body, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			return path.Base(fields[1]), nil
		}
	}
	return "", nil
}

// cargoPackageName returns the package name from Cargo.toml when present.
func cargoPackageName(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read Cargo.toml: %w", err)
	}
	inPackage := false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inPackage = line == "[package]"
			continue
		}
		if !inPackage || !strings.HasPrefix(line, "name") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "name" {
			return strings.Trim(strings.TrimSpace(val), `"'`), nil
		}
	}
	return "", nil
}

// csharpProjectName returns the project name when exactly one csproj exists.
func csharpProjectName(dir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.csproj"))
	if err != nil {
		return "", fmt.Errorf("find C# project files: %w", err)
	}
	if len(matches) != 1 {
		return "", nil
	}
	return strings.TrimSuffix(filepath.Base(matches[0]), filepath.Ext(matches[0])), nil
}
