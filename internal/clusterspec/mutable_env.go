// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterspec

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MutableEnv contains the variables delivered to vmconfig through mutable.env.
type MutableEnv map[string]string

// mutableEnvKeys defines the vmconfig mutable.env contract. Keep this in sync
// with vmconfig's default mutable.env file.
var mutableEnvKeys = []string{
	"SSH_AUTHORIZED_KEYS",
	"TELEMETRY_ENABLED",
	"TELEMETRY_LOG_CLOUDINIT",
	"TELEMETRY_LOG_SERVICES",
	"TELEMETRY_OTLP_ENDPOINT",
	"TELEMETRY_S3_BUCKET",
	"TELEMETRY_S3_REGION",
	"TELEMETRY_S3_ENDPOINT",
	"TELEMETRY_S3_ACCESS_KEY_ID",
	"TELEMETRY_S3_SECRET_ACCESS_KEY",
	"TELEMETRY_S3_ASSUME_ROLE",
	"OIDC_ISSUER",
	"OIDC_CA_CERT",
	"KUBE_API_ETCD_SERVERS",
	"KUBE_API_PUBLIC_HOSTNAME",
	"KUBE_API_INTERNAL_LB_HOSTNAME",
	"KUBE_API_PORT",
	"KUBE_LOG_LEVEL",
	"AWS_S3_USE_PATH_STYLE",
	"NETSY_BUCKET",
	"NETSY_REGION",
	"NETSY_ENDPOINT",
	"NETSY_ACCESS_KEY_ID",
	"NETSY_SECRET_ACCESS_KEY",
	"NETSY_ASSUME_ROLE",
	"REGISTRY_ENABLED",
	"REGISTRY_BUCKET",
	"REGISTRY_REGION",
	"REGISTRY_ENDPOINT",
	"REGISTRY_ACCESS_KEY_ID",
	"REGISTRY_SECRET_ACCESS_KEY",
	"REGISTRY_ASSUME_ROLE",
	"REGISTRY_HOSTNAME",
}

var serviceListRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(,[a-z0-9][a-z0-9-]*)*$`)

// SetObjectStorageEndpoint sets all component object storage endpoints to the
// same value. Use direct field assignment when components use different stores.
func (e MutableEnv) SetObjectStorageEndpoint(endpoint string) {
	e["NETSY_ENDPOINT"] = endpoint
	e["TELEMETRY_S3_ENDPOINT"] = endpoint
	e["REGISTRY_ENDPOINT"] = endpoint
}

// SetObjectStorageRegion sets all component object storage regions to the same
// value. Use direct field assignment when components use different stores.
func (e MutableEnv) SetObjectStorageRegion(region string) {
	e["NETSY_REGION"] = region
	e["TELEMETRY_S3_REGION"] = region
	e["REGISTRY_REGION"] = region
}

// Render validates and renders the mutable.env file delivered to vmconfig.
func (e MutableEnv) Render() (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	lines := make([]string, 0, len(mutableEnvKeys))
	for _, key := range mutableEnvKeys {
		lines = append(lines, key+"="+quoteEnvValue(e[key]))
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// ApplyDefaults populates mutable environment defaults and values derived from clusterID.
func (e MutableEnv) ApplyDefaults(clusterID string) {
	defaults := map[string]string{
		"KUBE_LOG_LEVEL":          "2",
		"KUBE_API_PORT":           "6443",
		"TELEMETRY_ENABLED":       "false",
		"TELEMETRY_LOG_CLOUDINIT": "true",
		"REGISTRY_ENABLED":        "true",
	}
	for key, value := range defaults {
		if e[key] == "" {
			e[key] = value
		}
	}
	if clusterID == "" {
		return
	}
	for key, suffix := range map[string]string{
		"NETSY_BUCKET":        "netsy",
		"REGISTRY_BUCKET":     "registry",
		"TELEMETRY_S3_BUCKET": "telemetry",
	} {
		if e[key] == "" {
			e[key] = fmt.Sprintf("%s-%s", clusterID, suffix)
		}
	}
}

// Validate checks values delivered through mutable.env.
func (e MutableEnv) Validate() error {
	for key, value := range e {
		if strings.ContainsAny(value, "'\n\r") {
			return fmt.Errorf("mutable environment variable %s contains a quote or newline", key)
		}
	}
	if _, err := strconv.ParseUint(e["KUBE_LOG_LEVEL"], 10, 64); err != nil {
		return fmt.Errorf("KUBE_LOG_LEVEL must be a non-negative integer")
	}
	port, err := strconv.ParseUint(e["KUBE_API_PORT"], 10, 16)
	if err != nil || port == 0 {
		return fmt.Errorf("KUBE_API_PORT must be an integer between 1 and 65535")
	}
	for _, key := range []string{"TELEMETRY_LOG_CLOUDINIT", "TELEMETRY_ENABLED", "REGISTRY_ENABLED"} {
		if e[key] != "true" && e[key] != "false" {
			return fmt.Errorf("%s must be either true or false", key)
		}
	}
	if services := e["TELEMETRY_LOG_SERVICES"]; services != "" && !serviceListRE.MatchString(services) {
		return fmt.Errorf("TELEMETRY_LOG_SERVICES must be a comma-separated list of lowercase service names")
	}
	if e["REGISTRY_ENABLED"] == "true" && e["REGISTRY_HOSTNAME"] == "" {
		return fmt.Errorf("REGISTRY_HOSTNAME is required when REGISTRY_ENABLED is true")
	}
	return nil
}

// quoteEnvValue quotes a value using vmconfig's env-file shell format.
func quoteEnvValue(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
