// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package netsyseed

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/netsy-dev/netsy/pkg/datafile"
	"github.com/podplane/podplane/internal/clusterconfig"
)

const (
	envoyGatewayKey = "/registry/gateway.networking.k8s.io/gateways/platform-envoy-gateway/platform-envoy-gateway"
	envoyProxyKey   = "/registry/gateway.envoyproxy.io/envoyproxies/platform-envoy-gateway/platform-envoy-gateway"
	envoySPCKey     = "/registry/secrets-store.csi.x-k8s.io/secretproviderclasses/platform-envoy-gateway/platform-envoy-gateway-ingress-certificates"
	envoySecretBase = "/registry/secrets/platform-envoy-gateway/bundle-"
)

// interpolateEnvoyIngress replaces captured ingress-domain configuration with
// configuration derived from the target cluster and synthesizes the
// non-sensitive SDS reference Secrets intentionally omitted from generic seeds.
func interpolateEnvoyIngress(records []*datafile.Record, cfg *clusterconfig.ClusterConfig) ([]*datafile.Record, error) {
	if len(cfg.Cluster.Domains) == 0 {
		return records, nil
	}

	var gateway, proxy, spc *datafile.Record
	for _, record := range records {
		switch string(record.Key) {
		case envoyGatewayKey:
			gateway = record
		case envoyProxyKey:
			proxy = record
		case envoySPCKey:
			spc = record
		default:
			if strings.HasPrefix(string(record.Key), envoySecretBase) {
				return nil, fmt.Errorf("generic seed unexpectedly contains domain-specific Envoy SDS Secret %s", record.Key)
			}
		}
	}
	if gateway == nil && proxy == nil && spc == nil {
		return records, nil
	}
	if gateway == nil || proxy == nil || spc == nil {
		return nil, fmt.Errorf("podplane seed file contains incomplete Envoy ingress resources")
	}

	domains := make([]string, len(cfg.Cluster.Domains))
	for i, domain := range cfg.Cluster.Domains {
		domains[i] = domain.Zone
	}
	if err := rewriteEnvoyGateway(gateway, domains); err != nil {
		return nil, err
	}
	if err := rewriteEnvoyProxy(proxy, domains); err != nil {
		return nil, err
	}
	if err := rewriteEnvoySPC(spc, cfg, domains); err != nil {
		return nil, err
	}

	var revision int64
	for _, record := range records {
		if record.Revision > revision {
			revision = record.Revision
		}
	}
	for _, domain := range domains {
		revision++
		record, err := envoySDSSecretRecord(domain, revision, cfg.Cluster.ID)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// rewriteEnvoyGateway replaces captured HTTPS listeners with listeners for the
// target cluster's domains while preserving non-HTTPS listeners.
func rewriteEnvoyGateway(record *datafile.Record, domains []string) error {
	obj, err := decodeEnvoyRecord(record, "Gateway")
	if err != nil {
		return err
	}
	spec, err := mapField(obj, "spec", envoyGatewayKey)
	if err != nil {
		return err
	}
	listeners, ok := spec["listeners"].([]any)
	if !ok {
		return fmt.Errorf("record at %s has malformed spec.listeners", envoyGatewayKey)
	}
	kept := make([]any, 0, len(listeners)+2*len(domains))
	for _, value := range listeners {
		listener, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("record at %s has malformed listener", envoyGatewayKey)
		}
		if stringValue(listener["protocol"]) != "HTTPS" {
			kept = append(kept, listener)
		}
	}
	for _, domain := range domains {
		bundle := envoyBundleName(domain)
		suffix := envoyListenerSuffix(domain)
		kept = append(kept,
			envoyHTTPSListener("https-"+suffix, domain, bundle),
			envoyHTTPSListener("https-wildcard-"+suffix, "*."+domain, bundle),
		)
	}
	spec["listeners"] = kept
	return encodeEnvoyRecord(record, obj)
}

// envoyHTTPSListener returns an HTTPS Gateway listener backed by bundle.
func envoyHTTPSListener(name, hostname, bundle string) map[string]any {
	if len(name) > 63 {
		name = strings.TrimSuffix(name[:63], "-")
	}
	return map[string]any{
		"name":     name,
		"protocol": "HTTPS",
		"port":     float64(443),
		"hostname": hostname,
		"tls": map[string]any{
			"mode": "Terminate",
			"certificateRefs": []any{map[string]any{
				"group": "", "kind": "Secret", "name": bundle,
			}},
		},
		"allowedRoutes": map[string]any{"namespaces": map[string]any{"from": "All"}},
	}
}

// rewriteEnvoyProxy replaces captured SDS certificate arguments with arguments
// for the target cluster's domains.
func rewriteEnvoyProxy(record *datafile.Record, domains []string) error {
	obj, err := decodeEnvoyRecord(record, "EnvoyProxy")
	if err != nil {
		return err
	}
	containers, err := nestedSlice(obj, envoyProxyKey, "spec", "provider", "kubernetes", "envoyDaemonSet", "patch", "value", "spec", "template", "spec", "containers")
	if err != nil {
		return err
	}
	for _, value := range containers {
		container, ok := value.(map[string]any)
		if !ok || stringValue(container["name"]) != "podplane-sds" {
			continue
		}
		args, ok := container["args"].([]any)
		if !ok {
			return fmt.Errorf("record at %s has malformed podplane-sds args", envoyProxyKey)
		}
		kept := make([]any, 0, len(args)+len(domains))
		for _, arg := range args {
			if !strings.HasPrefix(stringValue(arg), "--certificate=") {
				kept = append(kept, arg)
			}
		}
		for _, domain := range domains {
			kept = append(kept, "--certificate="+envoyBundleName(domain)+"="+domain)
		}
		container["args"] = kept
		return encodeEnvoyRecord(record, obj)
	}
	return fmt.Errorf("record at %s does not contain the podplane-sds container", envoyProxyKey)
}

// rewriteEnvoySPC replaces captured provider parameters with parameters for the
// target cluster's certificate provider and domains.
func rewriteEnvoySPC(record *datafile.Record, cfg *clusterconfig.ClusterConfig, domains []string) error {
	obj, err := decodeEnvoyRecord(record, "SecretProviderClass")
	if err != nil {
		return err
	}
	delivery, err := ingressCertificateDeliveryValues(cfg)
	if err != nil {
		return err
	}
	spec, err := mapField(obj, "spec", envoySPCKey)
	if err != nil {
		return err
	}
	provider := stringValue(delivery["provider"])
	parameters := map[string]any{}
	var objects strings.Builder
	keyPrefix := strings.Trim(stringValue(delivery["keyPrefix"]), "/")
	for _, domain := range domains {
		bundle := envoyBundleName(domain)
		switch provider {
		case "aws":
			objectType := stringValue(delivery["objectType"])
			fmt.Fprintf(&objects, "- objectName: %s\n  objectType: %s\n  objectAlias: %s\n",
				strconv.Quote("/"+keyPrefix+"/platform-cluster/ingress-certificates/"+bundle), strconv.Quote(objectType), strconv.Quote(bundle))
		case "gcp":
			resource := fmt.Sprintf("projects/%s/secrets/%s_platform-cluster_ingress-certificates_%s/versions/latest",
				stringValue(delivery["projectID"]), strings.Trim(keyPrefix, "_"), bundle)
			fmt.Fprintf(&objects, "- resourceName: %s\n  path: %s\n", strconv.Quote(resource), strconv.Quote(bundle))
		case "vault", "openbao":
			mountPath := stringValue(delivery["mountPath"])
			if mountPath == "" {
				mountPath = "secret"
			}
			fmt.Fprintf(&objects, "- objectName: %s\n  secretPath: %s\n  secretKey: value\n",
				strconv.Quote(bundle), strconv.Quote(strings.Trim(mountPath, "/")+"/data/"+keyPrefix+"/platform-cluster/ingress-certificates/"+bundle))
		default:
			return fmt.Errorf("unsupported Envoy ingress certificate provider %q", provider)
		}
	}
	parameters["objects"] = strings.TrimSuffix(objects.String(), "\n")
	switch provider {
	case "aws", "gcp":
	case "vault":
		parameters["vaultAddress"] = delivery["address"]
		parameters["vaultAuthMountPath"] = valueOrDefault(delivery, "authMountPath", "kubernetes")
		parameters["roleName"] = "platform-envoy-gateway-ingress-certificates"
		if ca := stringValue(delivery["caCertPath"]); ca != "" {
			parameters["vaultCACertPath"] = ca
		}
	case "openbao":
		parameters["baoAddress"] = delivery["address"]
		parameters["baoAuthMountPath"] = valueOrDefault(delivery, "authMountPath", "kubernetes")
		parameters["roleName"] = "platform-envoy-gateway-ingress-certificates"
		if ca := stringValue(delivery["caCertPath"]); ca != "" {
			parameters["baoCACertPath"] = ca
		}
	}
	spec["provider"] = provider
	spec["parameters"] = parameters
	return encodeEnvoyRecord(record, obj)
}

// envoySDSSecretRecord constructs the non-sensitive SDS reference Secret record
// for one ingress domain.
func envoySDSSecretRecord(domain string, revision int64, leaderID string) (*datafile.Record, error) {
	bundle := envoyBundleName(domain)
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":            bundle,
			"namespace":       "platform-envoy-gateway",
			"resourceVersion": strconv.FormatInt(revision, 10),
			"uid":             envoySecretUID(domain),
			"annotations": map[string]any{
				"meta.helm.sh/release-name":      "platform-envoy-gateway",
				"meta.helm.sh/release-namespace": "platform-envoy-gateway",
			},
			"labels": map[string]any{
				"app.kubernetes.io/managed-by":     "Helm",
				"helm.toolkit.fluxcd.io/name":      "envoy-gateway",
				"helm.toolkit.fluxcd.io/namespace": "platform-envoy-gateway",
			},
		},
		"type": "gateway.envoyproxy.io/sds",
		"data": map[string]any{
			"url":        base64.StdEncoding.EncodeToString([]byte("unix:///var/run/podplane-sds/sds.sock")),
			"secretName": base64.StdEncoding.EncodeToString([]byte(bundle)),
		},
	}
	value, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("encode Envoy SDS Secret for domain %s: %w", domain, err)
	}
	return &datafile.Record{
		Revision: revision, Key: []byte(envoySecretBase + strings.TrimPrefix(bundle, "bundle-")), Value: value,
		Created: true, CreateRevision: revision, Version: 1, LeaderID: leaderID,
	}, nil
}

// envoyBundleName returns the domain-derived SDS bundle name used by the Envoy
// Gateway Helm chart.
func envoyBundleName(domain string) string {
	sum := sha256.Sum256([]byte(domain))
	return "bundle-" + hex.EncodeToString(sum[:])[:24]
}

// envoyListenerSuffix returns the domain-derived listener suffix used by the
// Envoy Gateway Helm chart.
func envoyListenerSuffix(domain string) string {
	var b strings.Builder
	previousDash := false
	for _, r := range strings.ToLower(domain) {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-'
		if valid {
			b.WriteRune(r)
			previousDash = r == '-'
		} else if !previousDash {
			b.WriteByte('-')
			previousDash = true
		}
	}
	name := b.String()
	if len(name) > 25 {
		name = name[:25]
	}
	name = strings.Trim(name, "-")
	sum := sha256.Sum256([]byte(domain))
	return name + "-" + hex.EncodeToString(sum[:])[:8]
}

// envoySecretUID returns a stable UID for a synthesized domain SDS Secret.
func envoySecretUID(domain string) string {
	sum := sha256.Sum256([]byte("podplane-envoy-sds-secret:" + domain))
	h := hex.EncodeToString(sum[:16])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// decodeEnvoyRecord decodes record and verifies its Kubernetes kind.
func decodeEnvoyRecord(record *datafile.Record, kind string) (map[string]any, error) {
	var obj map[string]any
	if err := json.Unmarshal(record.Value, &obj); err != nil {
		return nil, fmt.Errorf("decode %s at %s: %w", kind, record.Key, err)
	}
	if stringValue(obj["kind"]) != kind {
		return nil, fmt.Errorf("record at %s is not a %s", record.Key, kind)
	}
	return obj, nil
}

// encodeEnvoyRecord encodes obj into record's value.
func encodeEnvoyRecord(record *datafile.Record, obj map[string]any) error {
	value, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("encode record %s: %w", record.Key, err)
	}
	record.Value = value
	return nil
}

// nestedSlice returns the array at path or reports a malformed record.
func nestedSlice(obj map[string]any, context string, path ...string) ([]any, error) {
	current := obj
	for _, key := range path[:len(path)-1] {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("record at %s has malformed %s", context, strings.Join(path, "."))
		}
		current = next
	}
	values, ok := current[path[len(path)-1]].([]any)
	if !ok {
		return nil, fmt.Errorf("record at %s has malformed %s", context, strings.Join(path, "."))
	}
	return values, nil
}

// valueOrDefault returns a non-empty string value or fallback.
func valueOrDefault(values map[string]any, key, fallback string) string {
	if value := stringValue(values[key]); value != "" {
		return value
	}
	return fallback
}
