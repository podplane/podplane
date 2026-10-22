// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"github.com/podplane/podplane/internal/execwrap"
	"github.com/podplane/podplane/internal/kubectl"
)

const (
	// APIGroupVersion is the Podplane Secrets aggregated API group/version.
	APIGroupVersion = "secrets-api.podplane.dev/v1beta1"
)

// PublicKey is the Kubernetes-style public key object returned by the operator.
type PublicKey struct {
	APIVersion string `json:"apiVersion,omitempty" yaml:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty" yaml:"kind,omitempty"`
	Metadata   struct {
		Name string `json:"name,omitempty" yaml:"name,omitempty"`
	} `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Spec PublicKeySpec `json:"spec" yaml:"spec"`
}

// PublicKeySpec describes the current operator encryption public key.
type PublicKeySpec struct {
	KeyID     string `json:"keyID" yaml:"keyID"`
	CreatedAt string `json:"createdAt,omitempty" yaml:"createdAt,omitempty"`
	Algorithm string `json:"algorithm" yaml:"algorithm"`
	PublicKey string `json:"publicKey" yaml:"publicKey"`
}

// SecretProviderKeyspace is the Kubernetes-style SecretProviderKeyspace object.
type SecretProviderKeyspace struct {
	APIVersion string         `json:"apiVersion" yaml:"apiVersion"`
	Kind       string         `json:"kind" yaml:"kind"`
	Metadata   KeyspaceMeta   `json:"metadata" yaml:"metadata"`
	Spec       *KeyspaceSpec  `json:"spec,omitempty" yaml:"spec,omitempty"`
	Status     KeyspaceStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

// KeyspaceMeta identifies a SecretProviderKeyspace resource.
type KeyspaceMeta struct {
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Name      string `json:"name" yaml:"name"`
}

// KeyspaceSpec contains requested entry operations.
type KeyspaceSpec struct {
	Entries []EntryRequest `json:"entries" yaml:"entries"`
}

// EntryRequest is one SecretProviderKeyspace entry operation.
type EntryRequest struct {
	Key            string          `json:"key" yaml:"key"`
	Operation      string          `json:"operation" yaml:"operation"`
	Binary         bool            `json:"binary,omitempty" yaml:"binary,omitempty"`
	EncryptedValue *EncryptedValue `json:"encryptedValue,omitempty" yaml:"encryptedValue,omitempty"`
}

// EncryptedValue carries client-side encrypted secret material.
type EncryptedValue struct {
	KeyID      string `json:"keyID" yaml:"keyID"`
	Algorithm  string `json:"algorithm" yaml:"algorithm"`
	Ciphertext string `json:"ciphertext" yaml:"ciphertext"`
}

// KeyspaceStatus contains backend-derived metadata only.
type KeyspaceStatus struct {
	Provider string        `json:"provider,omitempty" yaml:"provider,omitempty"`
	Entries  []EntryStatus `json:"entries,omitempty" yaml:"entries,omitempty"`
}

// EntryStatus is one metadata-only backend key status.
type EntryStatus struct {
	Key          string `json:"key" yaml:"key"`
	Status       string `json:"status" yaml:"status"`
	BackendPath  string `json:"backendPath,omitempty" yaml:"backendPath,omitempty"`
	RestoreUntil string `json:"restoreUntil,omitempty" yaml:"restoreUntil,omitempty"`
}

// Client shells out to kubectl so aggregated API calls use the user's normal
// kubeconfig credentials and auth plugins.
type Client struct {
	Context    string
	Kubeconfig string
}

// WaitReady waits for the operator's aggregated API after its Helm release is ready.
func (c Client) WaitReady(ctx context.Context) error {
	args := append(kubectl.Args(c.Context, c.Kubeconfig), "wait", "--for=condition=Available", "apiservice/v1beta1.secrets-api.podplane.dev", "--timeout=5m")
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("wait for Secrets API: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// PublicKey fetches publickeys/latest.
func (c Client) PublicKey() (PublicKey, error) {
	var key PublicKey
	args := append(kubectl.Args(c.Context, c.Kubeconfig), "--raw", "/apis/"+APIGroupVersion+"/publickeys/latest")
	data, err := c.kubectl("get", args, nil)
	if err != nil {
		return key, err
	}
	if err := json.Unmarshal(data, &key); err != nil {
		return key, fmt.Errorf("decode public key response: %w", err)
	}
	if key.Spec.KeyID == "" || key.Spec.PublicKey == "" {
		return key, fmt.Errorf("publickeys/latest response is missing keyID or publicKey")
	}
	return key, nil
}

// Get returns metadata for one provider/SPC boundary.
func (c Client) Get(namespace, keyspaceName string) (SecretProviderKeyspace, error) {
	var keyspace SecretProviderKeyspace
	args := append(kubectl.Args(c.Context, c.Kubeconfig), "--raw", keyspacePath(namespace, keyspaceName, ""))
	data, err := c.kubectl("get", args, nil)
	if err != nil {
		return keyspace, err
	}
	if err := json.Unmarshal(data, &keyspace); err != nil {
		return keyspace, fmt.Errorf("decode SecretProviderKeyspace response: %w", err)
	}
	return keyspace, nil
}

// Put sends a named SecretProviderKeyspace update.
func (c Client) Put(keyspace SecretProviderKeyspace) (SecretProviderKeyspace, error) {
	var out SecretProviderKeyspace
	body, err := json.Marshal(keyspace)
	if err != nil {
		return out, fmt.Errorf("encode SecretProviderKeyspace request: %w", err)
	}
	args := append(kubectl.Args(c.Context, c.Kubeconfig), "--raw", keyspacePath(keyspace.Metadata.Namespace, keyspace.Metadata.Name, ""), "-f", "-")
	data, err := c.kubectl("replace", args, body)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("decode SecretProviderKeyspace response: %w", err)
	}
	return out, nil
}

// Delete archives or destroys keys under one provider/SPC boundary.
func (c Client) Delete(namespace, keyspaceName, key string, destroy bool) error {
	query := url.Values{}
	if key != "" {
		query.Set("key", key)
	}
	if destroy {
		query.Set("destroy", "true")
	}
	args := append(kubectl.Args(c.Context, c.Kubeconfig), "--raw", keyspacePath(namespace, keyspaceName, query.Encode()))
	_, err := c.kubectl("delete", args, nil)
	return err
}

// kubectl runs one kubectl command and returns stdout or a contextual error.
func (c Client) kubectl(verb string, args []string, stdin []byte) ([]byte, error) {
	all := append([]string{verb}, args...)
	cmd := execwrap.Command("kubectl", all...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("kubectl %s: %w: %s", verb, err, msg)
		}
		return nil, fmt.Errorf("kubectl %s: %w", verb, err)
	}
	return stdout.Bytes(), nil
}

// KeyspaceName returns the SecretProviderKeyspace name for provider and binding/SPC.
func KeyspaceName(provider, spc string) string {
	return provider + "." + spc
}

// NewKeyspaceRequest builds a one-entry SecretProviderKeyspace request.
func NewKeyspaceRequest(namespace, keyspaceName, key, operation string, encrypted *EncryptedValue) SecretProviderKeyspace {
	return SecretProviderKeyspace{
		APIVersion: APIGroupVersion,
		Kind:       "SecretProviderKeyspace",
		Metadata:   KeyspaceMeta{Namespace: namespace, Name: keyspaceName},
		Spec:       &KeyspaceSpec{Entries: []EntryRequest{{Key: key, Operation: operation, EncryptedValue: encrypted}}},
	}
}

// keyspacePath returns the aggregated API path for one SecretProviderKeyspace.
func keyspacePath(namespace, keyspaceName, query string) string {
	path := "/apis/" + APIGroupVersion + "/namespaces/" + url.PathEscape(namespace) + "/secretproviderkeyspaces/" + url.PathEscape(keyspaceName)
	if query != "" {
		path += "?" + query
	}
	return path
}
