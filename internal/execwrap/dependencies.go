// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package execwrap

import (
	"fmt"
	"regexp"
	"strings"
)

type Dependency struct {
	Binary       string
	VersionArgs  []string
	VersionParse func(output string) (string, error)
	MinVersion   int
}

var rsyncVersionRegex = regexp.MustCompile(`^rsync\s+version\s+(\d+\.\d+\.\d+)`)

var Dependencies = map[string]Dependency{
	"kubectl": {
		Binary:      "kubectl",
		VersionArgs: []string{"version", "--client", "true"},
		VersionParse: func(output string) (string, error) {
			lines := strings.Split(output, "\n")
			for _, line := range lines {
				if strings.Contains(line, "Client Version: v") {
					return strings.TrimSpace(strings.Replace(line, "Client Version: v", "", 1)), nil
				}
			}
			return "", nil
		},
		MinVersion: 1,
	},
	"rsync": {
		Binary:      "rsync",
		VersionArgs: []string{"--version"},
		VersionParse: func(output string) (string, error) {
			// find line starting with "rsync version " or "rsync  version " and strip any from the first space.
			lines := strings.Split(output, "\n")
			for _, line := range lines {
				parts := rsyncVersionRegex.FindStringSubmatch(line)
				if len(parts) >= 2 {
					return parts[1], nil
				}
			}
			return "", fmt.Errorf("unable to determine rsync version")
		},
		MinVersion: 3, // TODO: should be 3.1.0+
	},
	"mkcert": {
		Binary:      "mkcert",
		VersionArgs: []string{"-version"},
		VersionParse: func(output string) (string, error) {
			version := strings.TrimSpace(output)
			version = strings.TrimPrefix(version, "v")
			if version == "" {
				return "", fmt.Errorf("unable to determine mkcert version")
			}
			return version, nil
		},
		MinVersion: 1,
	},
	"qemu-system-aarch64": qemuDependency("qemu-system-aarch64"),
	"qemu-system-x86_64":  qemuDependency("qemu-system-x86_64"),
	"qemu-img":            qemuDependency("qemu-img"),
}

func qemuDependency(binary string) Dependency {
	return Dependency{
		Binary:      binary,
		VersionArgs: []string{"--version"},
		VersionParse: func(output string) (string, error) {
			parts := regexp.MustCompile(`(\d+\.\d+\.\d+)`).FindStringSubmatch(output)
			if len(parts) < 2 {
				return "", fmt.Errorf("unable to determine qemu version")
			}
			return parts[1], nil
		},
		MinVersion: 9,
	}
}
