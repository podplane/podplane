// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package netsyseed

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/netsy-dev/netsy/pkg/datafile"
	"github.com/podplane/podplane/internal/clusterconfig"
)

// Seed records rendered from the captured cluster's Secrets providers.
//
// Podplane intentionally does not store sensitive material, such as private
// keys, API keys, or passwords, in Kubernetes Secrets. The records below hold
// only how to reach a provider: addresses, public CA certificates, auth and
// mount paths, key prefixes, and role names. Secret values such as the
// workload CA key stay in the provider and reach pods through CSI mounts;
// neither the seed nor this file carries them.
const (
	operatorConfigKey         = "/registry/configmaps/platform-podplane-operator/platform-podplane-operator-config"
	operatorSPCKey            = "/registry/secrets-store.csi.x-k8s.io/secretproviderclasses/platform-podplane-operator/platform-podplane-operator"
	providerCAConfigMapPrefix = "/registry/configmaps/platform-secrets-store-csi-provider-"
	providerCAConfigMapName   = "/podplane-secrets-provider-ca-"
)

// interpolateSecretsProviders replaces the captured cluster's Secrets provider
// settings in records that pods read only at startup: the Vault/OpenBao CSI
// provider CA ConfigMaps, the operator config, and the operator's workload CA
// SecretProviderClass. Flux renders the same resources later, but by then the
// pods have already started: the CSI provider caches its TLS config per
// SecretProviderClass, CSI volumes are mounted once, and the operator never
// reloads its config. Seeds may omit the CA ConfigMaps and the
// SecretProviderClass; they are created when missing.
func interpolateSecretsProviders(records []*datafile.Record, cfg *clusterconfig.ClusterConfig) ([]*datafile.Record, error) {
	missing, err := missingSecretsProviderRecords(records, cfg)
	if err != nil {
		return nil, err
	}
	secrets := cfg.Cluster.Secrets
	out := make([]*datafile.Record, 0, len(records)+len(missing))
	for _, record := range slices.Concat(records, missing) {
		switch key := string(record.Key); {
		case key == operatorConfigKey:
			err = rewriteRecord(record, func(obj map[string]any) error {
				return setOperatorSecrets(obj, secrets)
			})
		case key == operatorSPCKey:
			err = rewriteRecord(record, func(obj map[string]any) error {
				return setWorkloadCASecretProviderClass(obj, cfg.Cluster.ID, secrets)
			})
		case strings.HasPrefix(key, providerCAConfigMapPrefix) && strings.Contains(key, providerCAConfigMapName):
			ca, ok := providerCACert(key, secrets)
			if !ok {
				// The chart renders no CA ConfigMap for this provider.
				continue
			}
			err = rewriteRecord(record, func(obj map[string]any) error {
				ensureMap(obj, "data")["ca.crt"] = ca
				return nil
			})
		}
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

// missingSecretsProviderRecords returns empty records for the CA ConfigMaps and
// operator SecretProviderClass that the cluster needs but the seed lacks,
// ready for interpolateSecretsProviders to fill in. A record is only created
// when the seed has its namespace; otherwise Flux installs the chart, which
// creates it.
func missingSecretsProviderRecords(records []*datafile.Record, cfg *clusterconfig.ClusterConfig) ([]*datafile.Record, error) {
	keys := make(map[string]bool, len(records))
	for _, record := range records {
		keys[string(record.Key)] = true
	}
	var missing []*datafile.Record
	add := func(key, apiVersion, kind, namespace, name string) error {
		if keys[key] || !keys["/registry/namespaces/"+namespace] {
			return nil
		}
		record, err := helmOwnedRecord(key, apiVersion, kind, namespace, name, cfg.Cluster.ID)
		if err != nil {
			return err
		}
		missing = append(missing, record)
		return nil
	}
	if err := add(operatorSPCKey, "secrets-store.csi.x-k8s.io/v1", "SecretProviderClass", "platform-podplane-operator", "platform-podplane-operator"); err != nil {
		return nil, err
	}
	providers := cfg.Cluster.Secrets.Providers
	for _, name := range slices.Sorted(maps.Keys(providers)) {
		provider := providers[name]
		if provider.CACert == "" || (provider.Kind != "vault" && provider.Kind != "openbao") {
			continue
		}
		key := providerCAConfigMapPrefix + provider.Kind + providerCAConfigMapName + name
		if err := add(key, "v1", "ConfigMap", "platform-secrets-store-csi-provider-"+provider.Kind, "podplane-secrets-provider-ca-"+name); err != nil {
			return nil, err
		}
	}
	return missing, nil
}

// helmOwnedRecord returns a seed record holding only the identity and Helm
// ownership metadata of an object, so the release adopts it on its next
// upgrade. Platform components install each release into a namespace of the
// same name.
func helmOwnedRecord(key, apiVersion, kind, namespace, name, leaderID string) (*datafile.Record, error) {
	value, err := json.Marshal(map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"uid":       seedResourceUID(key),
			"annotations": map[string]any{
				"meta.helm.sh/release-name":      namespace,
				"meta.helm.sh/release-namespace": namespace,
			},
			"labels": map[string]any{"app.kubernetes.io/managed-by": "Helm"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode seed record %s: %w", key, err)
	}
	return &datafile.Record{Key: []byte(key), Value: value, Created: true, Version: 1, LeaderID: leaderID}, nil
}

// providerCACert returns the CA certificate the CSI provider chart renders
// into the ConfigMap at key, which is named after its provider kind and the
// Secrets provider it trusts. It reports false when the cluster has no such
// provider or the provider has no CA certificate.
func providerCACert(key string, secrets clusterconfig.Secrets) (string, bool) {
	kind, name, _ := strings.Cut(strings.TrimPrefix(key, providerCAConfigMapPrefix), providerCAConfigMapName)
	provider, ok := secrets.Providers[name]
	if !ok || provider.Kind != kind || provider.CACert == "" {
		return "", false
	}
	return provider.CACert, true
}

// setOperatorSecrets replaces the providers in the operator ConfigMap's
// config.json, keeping its other settings.
func setOperatorSecrets(configMap map[string]any, secrets clusterconfig.Secrets) error {
	data := ensureMap(configMap, "data")
	var config map[string]any
	if err := json.Unmarshal([]byte(stringValue(data["config.json"])), &config); err != nil {
		return fmt.Errorf("decode operator config: %w", err)
	}
	// The chart snake-cases the provider values, which yields the cluster
	// config's JSON field names.
	operatorSecrets := ensureMap(config, "secrets")
	operatorSecrets["default_provider"] = secrets.DefaultProvider
	operatorSecrets["providers"] = secrets.Providers
	contents, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode operator config: %w", err)
	}
	data["config.json"] = string(contents)
	return nil
}

// setWorkloadCASecretProviderClass replaces the provider and parameters of the
// operator's workload CA SecretProviderClass with those the podplane-operator
// chart renders for the cluster's default Secrets provider. Captured
// parameters name the captured cluster's backend and key prefix, so mounting
// them could read another cluster's workload CA key.
func setWorkloadCASecretProviderClass(spc map[string]any, clusterID string, secrets clusterconfig.Secrets) error {
	name := secrets.DefaultProvider
	provider, ok := secrets.Providers[name]
	if !ok {
		return fmt.Errorf("cluster.secrets.default_provider %q must select a configured provider for the operator workload CA", name)
	}
	keyPrefix := cmp.Or(provider.KeyPrefix, clusterID)
	parameters := map[string]any{}
	switch provider.Kind {
	case "aws":
		parameters["objects"] = fmt.Sprintf("- objectName: %s\n  objectType: %s\n  objectAlias: workload-ca-key",
			strconv.Quote("/"+strings.Trim(keyPrefix, "/")+"/workload-ca-key"), strconv.Quote(provider.ObjectType))
	case "gcp":
		parameters["auth"] = "provider-adc"
		parameters["secrets"] = fmt.Sprintf("- resourceName: %s\n  path: workload-ca-key",
			strconv.Quote("projects/"+provider.ProjectID+"/secrets/"+strings.Trim(keyPrefix, "_")+"_workload-ca-key/versions/latest"))
	case "vault", "openbao":
		// The Vault provider prefixes its parameters with vault, OpenBao with bao.
		prefix := "vault"
		if provider.Kind == "openbao" {
			prefix = "bao"
		}
		// Defaults apply before trimming, as in the chart.
		authPath := strings.Trim(cmp.Or(provider.AuthPath, "auth/kubernetes"), "/")
		mountPath := strings.Trim(cmp.Or(provider.MountPath, "secret"), "/")
		parameters[prefix+"Address"] = provider.Address
		parameters[prefix+"AuthMountPath"] = strings.TrimPrefix(authPath, "auth/")
		if provider.CACert != "" {
			parameters[prefix+"CACertPath"] = "/var/run/podplane/secrets-providers/" + name + "/ca.crt"
		}
		parameters["roleName"] = cmp.Or(provider.OperatorRole, "podplane-operator")
		parameters["objects"] = fmt.Sprintf("- objectName: workload-ca-key\n  secretPath: %s\n  secretKey: value",
			strconv.Quote(mountPath+"/data/"+strings.Trim(keyPrefix, "/")+"/workload-ca-key"))
	default:
		return fmt.Errorf("cluster.secrets.default_provider must select an aws, gcp, vault, or openbao provider, got %q", provider.Kind)
	}
	spec := ensureMap(spc, "spec")
	spec["provider"] = provider.Kind
	spec["parameters"] = parameters
	return nil
}

// rewriteRecord decodes a JSON seed record, applies edit, and stores the
// result back in the record.
func rewriteRecord(record *datafile.Record, edit func(map[string]any) error) error {
	var obj map[string]any
	if err := json.Unmarshal(record.Value, &obj); err != nil {
		return fmt.Errorf("decode seed record %s: %w", record.Key, err)
	}
	if err := edit(obj); err != nil {
		return fmt.Errorf("rewrite seed record %s: %w", record.Key, err)
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("encode seed record %s: %w", record.Key, err)
	}
	record.Value = encoded
	return nil
}
