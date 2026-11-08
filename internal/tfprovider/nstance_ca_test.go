// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nstance-dev/nstance/v2/pkg/nonce"
	"google.golang.org/api/option"
)

// TestNstanceCALifecycle exercises real SDK requests and framework state, with
// interrupted publication, adoption, mismatched material, and metadata-only refresh.
func TestNstanceCALifecycle(t *testing.T) {
	var mu sync.Mutex
	var key, certificate []byte
	failUpload, denyKeyRead := true, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if target := r.Header.Get("X-Amz-Target"); target != "" {
			w.Header().Set("Content-Type", "application/x-amz-json-1.1")
			switch target {
			case "AmazonSSM.PutParameter":
				var in struct {
					Name, Value, Type string
					Overwrite         bool
				}
				if json.NewDecoder(r.Body).Decode(&in) != nil || in.Name != "test-ca.key" || in.Type != "SecureString" || in.Overwrite {
					t.Error("unsafe CA key write")
					w.WriteHeader(400)
					return
				}
				if key != nil {
					w.WriteHeader(400)
					_, _ = io.WriteString(w, `{"__type":"ParameterAlreadyExists"}`)
					return
				}
				key = []byte(in.Value)
				_, _ = io.WriteString(w, `{"Version":3}`)
			case "AmazonSSM.GetParameter":
				if denyKeyRead || key == nil {
					w.WriteHeader(403)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"Parameter": map[string]any{"Name": "test-ca.key", "Value": string(key), "Type": "SecureString", "Version": 3}})
			case "AmazonSSM.DescribeParameters":
				if key == nil {
					_, _ = io.WriteString(w, `{"Parameters":[]}`)
					return
				}
				_, _ = io.WriteString(w, `{"Parameters":[{"Name":"test-ca.key","Type":"SecureString","Version":3}]}`)
			default:
				w.WriteHeader(400)
			}
			return
		}
		if r.URL.Path != "/ca-test/cluster/ca.crt" {
			t.Errorf("wrong certificate path: %s", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if certificate == nil {
				w.WriteHeader(404)
				_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code></Error>`)
				return
			}
			_, _ = w.Write(certificate)
		case http.MethodPut:
			if r.Header.Get("If-None-Match") != "*" {
				t.Error("certificate write can overwrite existing CA")
				w.WriteHeader(400)
				return
			}
			if failUpload {
				w.WriteHeader(403)
				return
			}
			if certificate != nil {
				w.WriteHeader(412)
				return
			}
			certificate, _ = io.ReadAll(r.Body)
			w.Header().Set("ETag", `"test-etag"`)
		default:
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	t.Setenv("AWS_ENDPOINT_URL_SSM", server.URL)
	t.Setenv("AWS_ENDPOINT_URL_S3", server.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_SESSION_TOKEN", "")
	r := NewNstanceCAResource()
	schema := resourceSchema(t, r)
	plan := tfsdk.Plan{Schema: schema}
	m := nstanceCAModel{Provider: types.StringValue(cloudSecretProviderAWSSSM), SecretPrefix: types.StringValue("test-"), Region: types.StringValue("us-east-1"), Bucket: types.StringValue("ca-test"), ClusterID: types.StringValue("test")}
	if d := plan.Set(t.Context(), m); d.HasError() {
		t.Fatal(d)
	}
	create := func() resource.CreateResponse {
		t.Helper()
		resp := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
		r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &resp)
		return resp
	}
	if resp := create(); !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "publication incomplete") {
		t.Fatalf("expected interrupted certificate publication, got %v", resp.Diagnostics)
	}
	mu.Lock()
	originalKey := bytes.Clone(key)
	failUpload = false
	mu.Unlock()
	if _, err := nonce.FingerprintSigningKey(originalKey); err != nil {
		t.Fatalf("Nstance rejected key format: %v", err)
	}
	created := create()
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	if d := created.State.Get(t.Context(), &m); d.HasError() {
		t.Fatal(d)
	}
	mu.Lock()
	originalCert := bytes.Clone(certificate)
	keyUnchanged := bytes.Equal(originalKey, key)
	mu.Unlock()
	if !keyUnchanged || m.Certificate.ValueString() != string(originalCert) || m.BackendVersion.ValueString() != "3" {
		t.Fatal("retry changed key or recorded incorrect metadata")
	}
	if strings.Contains(created.State.Raw.String(), "PRIVATE KEY") {
		t.Fatal("private key leaked into Terraform state")
	}
	block, _ := pem.Decode(originalCert)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if cert.PublicKeyAlgorithm != x509.Ed25519 || cert.SignatureAlgorithm != x509.PureEd25519 || !cert.IsCA || cert.MaxPathLen != 1 || cert.KeyUsage != x509.KeyUsageCertSign|x509.KeyUsageDigitalSignature || cert.CheckSignatureFrom(cert) != nil {
		t.Fatal("incompatible Nstance CA algorithm or constraints")
	}
	keyBlock, _ := pem.Decode(originalKey)
	parsedKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cert.PublicKey.(ed25519.PublicKey), parsedKey.(ed25519.PrivateKey).Public().(ed25519.PublicKey)) {
		t.Fatal("certificate/key mismatch")
	}
	adopted := create()
	if adopted.Diagnostics.HasError() || !adopted.State.Raw.Equal(created.State.Raw) {
		t.Fatalf("adoption changed CA: %v", adopted.Diagnostics)
	}
	mu.Lock()
	denyKeyRead = true
	mu.Unlock()
	refresh := resource.ReadResponse{State: created.State}
	r.Read(t.Context(), resource.ReadRequest{State: created.State}, &refresh)
	if refresh.Diagnostics.HasError() {
		t.Fatalf("refresh must not read private key: %v", refresh.Diagnostics)
	}
	mu.Lock()
	certificate = nil
	mu.Unlock()
	missing := resource.ReadResponse{State: created.State}
	r.Read(t.Context(), resource.ReadRequest{State: created.State}, &missing)
	if !missing.Diagnostics.HasError() {
		t.Fatal("refresh accepted a lost CA certificate")
	}
	mu.Lock()
	certificate = originalCert
	key = nil
	denyKeyRead = false
	mu.Unlock()
	orphaned := create()
	if !orphaned.Diagnostics.HasError() {
		t.Fatal("orphan certificate caused a replacement CA key")
	}
	mu.Lock()
	if key != nil {
		t.Error("orphan CA key was recreated")
	}
	key, _ = nonce.GenerateSigningKey()
	mu.Unlock()
	mismatched := create()
	if !mismatched.Diagnostics.HasError() {
		t.Fatal("mismatched CA pair accepted")
	}
	deleted := resource.DeleteResponse{State: created.State}
	r.Delete(t.Context(), resource.DeleteRequest{State: created.State}, &deleted)
	mu.Lock()
	defer mu.Unlock()
	if key == nil || !bytes.Equal(certificate, originalCert) {
		t.Fatal("destroy removed CA material")
	}
}

// TestNstanceCAGoogle checks create-only publication and recovery when GCS
// committed an upload but the response was lost.
func TestNstanceCAGoogle(t *testing.T) {
	var certificate []byte
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodGet {
			if !strings.HasSuffix(r.URL.Path, "/cluster/ca.crt") {
				t.Errorf("wrong CA read path: %s", r.URL.Path)
			}
			if certificate == nil {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write(certificate)
			return
		}
		if r.Method != http.MethodPost || r.URL.Query().Get("ifGenerationMatch") != "0" {
			t.Error("unsafe GCS CA upload")
			w.WriteHeader(400)
			return
		}
		if certificate != nil {
			w.WriteHeader(412)
			return
		}
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		parts := multipart.NewReader(r.Body, params["boundary"])
		metadata, err := parts.NextPart()
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var attrs struct{ Name string }
		if json.NewDecoder(metadata).Decode(&attrs) != nil || attrs.Name != "cluster/ca.crt" {
			t.Error("incorrect GCS CA object name")
			w.WriteHeader(400)
			return
		}
		part, err := parts.NextPart()
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		certificate, _ = io.ReadAll(part)
		w.WriteHeader(412) // Simulate committed write with failed response.
	}))
	defer server.Close()
	client, err := storage.NewClient(t.Context(), option.WithEndpoint(server.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	certs := &gcsCACertificateStore{client: client, bucket: "nstance-test"}
	keys, _ := registrationStore(t, cloudSecretProviderGCPSecretManager)
	first, cert, err := provisionNstanceCA(t.Context(), keys, certs, "test")
	if err != nil {
		t.Fatal(err)
	}
	second, adopted, err := provisionNstanceCA(t.Context(), keys, certs, "test")
	if err != nil || first != second || !bytes.Equal(cert, adopted) {
		t.Fatalf("GCS CA recovery changed identity: %v", err)
	}
}
