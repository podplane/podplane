// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-jose/go-jose/v4"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestServiceAccountKeyLifecycle checks durable adoption, public-key interoperability,
// metadata-only refresh, and retention without private material in Terraform state.
func TestServiceAccountKeyLifecycle(t *testing.T) {
	for _, algorithm := range []jose.SignatureAlgorithm{jose.ES256, jose.RS256} {
		t.Run(string(algorithm), func(t *testing.T) {
			const name = "/cluster/service-accounts.key"
			var mu sync.Mutex
			var value string
			if algorithm == jose.RS256 {
				key, err := rsa.GenerateKey(rand.Reader, 2048)
				if err != nil {
					t.Fatal(err)
				}
				der, err := x509.MarshalPKCS8PrivateKey(key)
				if err != nil {
					t.Fatal(err)
				}
				value = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
			}
			original := value
			version := 1
			forbidReads := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/x-amz-json-1.1")
				switch req.Header.Get("X-Amz-Target") {
				case "AmazonSSM.PutParameter":
					var in struct {
						Name, Value, Type string
						Overwrite         bool
					}
					if json.NewDecoder(req.Body).Decode(&in) != nil || in.Name != name || in.Type != "SecureString" || in.Overwrite {
						t.Error("invalid create-only secret request")
						w.WriteHeader(400)
						return
					}
					if value != "" {
						w.WriteHeader(400)
						_, _ = w.Write([]byte(`{"__type":"ParameterAlreadyExists"}`))
						return
					}
					value = in.Value
					_, _ = w.Write([]byte(`{"Version":1}`))
				case "AmazonSSM.GetParameter":
					if forbidReads {
						t.Error("refresh read the private key")
						w.WriteHeader(403)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Parameter": map[string]any{"Name": name, "Type": "SecureString", "Value": value, "Version": version}})
				case "AmazonSSM.DescribeParameters":
					_ = json.NewEncoder(w).Encode(map[string]any{"Parameters": []any{map[string]any{"Name": name, "Type": "SecureString", "Version": version}}})
				default:
					t.Error("unexpected backend operation")
					w.WriteHeader(400)
				}
			}))
			defer server.Close()
			t.Setenv("AWS_ENDPOINT_URL_SSM", server.URL)
			t.Setenv("AWS_ACCESS_KEY_ID", "fake-key")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "fake-secret")
			t.Setenv("AWS_SESSION_TOKEN", "")
			ctx := context.Background()
			r := &serviceAccountKeyResource{}
			sch := resourceSchema(t, r)
			plan := tfsdk.Plan{Schema: sch}
			m := serviceAccountKeyModel{Provider: types.StringValue(cloudSecretProviderAWSSSM), SecretPrefix: types.StringValue("/cluster/"), Region: types.StringValue("us-east-1")}
			if d := plan.Set(ctx, m); d.HasError() {
				t.Fatal(d)
			}
			created := resource.CreateResponse{State: tfsdk.State{Schema: sch}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
			if created.Diagnostics.HasError() {
				t.Fatal(created.Diagnostics)
			}
			if d := created.State.Get(ctx, &m); d.HasError() {
				t.Fatal(d)
			}
			if m.ID.ValueString() != "aws-ssm:"+name || m.BackendVersion.ValueString() != "1" || strings.Contains(created.State.Raw.String(), "PRIVATE KEY") {
				t.Fatal("incorrect or sensitive Terraform state")
			}
			mu.Lock()
			privatePEM := value
			mu.Unlock()
			block, _ := pem.Decode([]byte(privatePEM))
			private, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			if algorithm == jose.ES256 {
				key, ok := private.(*ecdsa.PrivateKey)
				if !ok || key.Curve != elliptic.P256() {
					t.Fatal("new signer must be ECDSA P-256")
				}
			} else if privatePEM != original {
				t.Fatal("replaced an existing RSA signer")
			}
			public, _ := pem.Decode([]byte(m.PublicKeyPEM.ValueString()))
			verificationDER, err := x509.MarshalPKIXPublicKey(private.(crypto.Signer).Public())
			if err != nil || !bytes.Equal(public.Bytes, verificationDER) {
				t.Fatal("PEM verification key does not match the signer", err)
			}
			var jwks jose.JSONWebKeySet
			if err := json.Unmarshal([]byte(m.JWKS.ValueString()), &jwks); err != nil {
				t.Fatal(err)
			}
			id := sha256.Sum256(public.Bytes)
			if len(jwks.Keys) != 1 || jwks.Keys[0].KeyID != base64.RawURLEncoding.EncodeToString(id[:]) || jwks.Keys[0].Algorithm != string(algorithm) || !jwks.Keys[0].IsPublic() {
				t.Fatal("JWKS does not match Kubernetes's verification key contract")
			}
			signer, err := jose.NewSigner(jose.SigningKey{Algorithm: algorithm, Key: private}, nil)
			if err != nil {
				t.Fatal(err)
			}
			const payload = `{"sub":"system:serviceaccount:platform-zot-registry:zot-registry"}`
			token, err := signer.Sign([]byte(payload))
			if err != nil {
				t.Fatal(err)
			}
			verified, err := token.Verify(jwks.Keys[0].Key)
			if err != nil || string(verified) != payload {
				t.Fatal("JWKS cannot verify a signed token", err)
			}
			adopted := resource.CreateResponse{State: tfsdk.State{Schema: sch}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &adopted)
			if adopted.Diagnostics.HasError() || !created.State.Raw.Equal(adopted.State.Raw) {
				t.Fatal("adoption changed the signer", adopted.Diagnostics)
			}
			mu.Lock()
			forbidReads = true
			mu.Unlock()
			read := resource.ReadResponse{State: created.State}
			r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
			if read.Diagnostics.HasError() {
				t.Fatal(read.Diagnostics)
			}
			mu.Lock()
			version++
			mu.Unlock()
			r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
			if !read.Diagnostics.HasError() {
				t.Fatal("accepted an out-of-band key change")
			}
			deleted := resource.DeleteResponse{State: created.State}
			r.Delete(ctx, resource.DeleteRequest{}, &deleted)
			mu.Lock()
			defer mu.Unlock()
			if !deleted.State.Raw.IsNull() || value != privatePEM {
				t.Fatal("destroy did not retain the signer")
			}
		})
	}
}
