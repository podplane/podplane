// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
	"google.golang.org/api/secretmanager/v1"
)

// TestGoogleServiceAccountKeyLifecycle checks reservation, adoption, safe publication failure and metadata-only refresh.
func TestGoogleServiceAccountKeyLifecycle(t *testing.T) {
	for name, failPublish := range map[string]bool{"create and adopt": false, "recover uncertain publication": true} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const secret = "projects/123/secrets/bootstrap"
			const email = "writer@example.iam.gserviceaccount.com"
			private, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				t.Fatal(err)
			}
			der, err := x509.MarshalPKCS8PrivateKey(private)
			if err != nil {
				t.Fatal(err)
			}
			privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
			encoded, err := json.Marshal(map[string]string{"type": "service_account", "client_email": email, "private_key_id": "abc123", "private_key": privatePEM})
			if err != nil {
				t.Fatal(err)
			}
			payload := string(encoded)
			reserved, published, disabled, missing, forbid := false, false, false, false, false
			issued, accesses := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
				switch {
				case req.Method == "DELETE":
					t.Error("unexpected deletion")
					w.WriteHeader(400)
				case req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/secrets"):
					if reserved {
						w.WriteHeader(409)
						return
					}
					reserved = true
					write(map[string]any{"name": secret})
				case req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/keys"):
					if !reserved {
						t.Error("issued before reservation")
					}
					issued++
					write(map[string]any{"privateKeyData": base64.StdEncoding.EncodeToString([]byte(payload))})
				case strings.HasSuffix(req.URL.Path, ":addVersion"):
					var in secretmanager.AddSecretVersionRequest
					if json.NewDecoder(req.Body).Decode(&in) != nil || in.Payload.Data != base64.StdEncoding.EncodeToString([]byte(payload)) {
						t.Error("wrong publication payload")
					}
					published = true
					if failPublish {
						w.WriteHeader(400)
						write(map[string]any{"error": map[string]any{"message": payload}})
						return
					}
					write(map[string]any{"name": secret + "/versions/1"})
				case strings.HasSuffix(req.URL.Path, ":access"):
					accesses++
					if forbid {
						t.Error("refresh read payload")
					}
					if !published {
						w.WriteHeader(404)
						return
					}
					write(map[string]any{"name": secret + "/versions/1", "payload": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte(payload))}})
				case strings.Contains(req.URL.Path, "/keys/"):
					if missing {
						w.WriteHeader(404)
						return
					}
					write(map[string]any{"keyType": "USER_MANAGED", "disabled": disabled})
				case strings.Contains(req.URL.Path, "/versions/"):
					write(map[string]any{"name": secret + "/versions/1", "state": "ENABLED"})
				default:
					write(map[string]any{"name": secret})
				}
			}))
			defer server.Close()
			opts := []option.ClientOption{option.WithEndpoint(server.URL + "/"), option.WithHTTPClient(server.Client())}
			i, err := iam.NewService(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			s, err := secretmanager.NewService(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			r := &googleServiceAccountKeyResource{iam: i, secrets: s}
			sch := resourceSchema(t, r)
			plan := tfsdk.Plan{Schema: sch}
			m := googleServiceAccountKeyModel{Project: types.StringValue("example"), ServiceAccount: types.StringValue(email), SecretID: types.StringValue("bootstrap")}
			if d := plan.Set(ctx, m); d.HasError() {
				t.Fatal(d)
			}
			create := func() resource.CreateResponse {
				out := resource.CreateResponse{State: tfsdk.State{Schema: sch}}
				r.Create(ctx, resource.CreateRequest{Plan: plan}, &out)
				return out
			}
			first := create()
			if first.Diagnostics.HasError() != failPublish {
				t.Fatal(first.Diagnostics)
			}
			if strings.Contains(fmt.Sprint(first.Diagnostics), "PRIVATE KEY") {
				t.Fatal("payload in diagnostics")
			}
			adopt := create()
			if adopt.Diagnostics.HasError() || issued != 1 {
				t.Fatalf("adoption: %v issued %d", adopt.Diagnostics, issued)
			}
			if strings.Contains(adopt.State.Raw.String(), "PRIVATE KEY") || strings.Contains(adopt.State.Raw.String(), "private_key") {
				t.Fatal("payload in state")
			}
			if d := adopt.State.Get(ctx, &m); d.HasError() {
				t.Fatal(d)
			}
			if m.ID.ValueString() != secret || m.SecretVersion.ValueString() != secret+"/versions/1" {
				t.Fatal("wrong metadata")
			}
			forbid = true
			before := accesses
			read := resource.ReadResponse{State: adopt.State}
			r.Read(ctx, resource.ReadRequest{State: adopt.State}, &read)
			if read.Diagnostics.HasError() || accesses != before {
				t.Fatal(read.Diagnostics)
			}
			for _, mode := range []string{"disabled", "missing"} {
				disabled, missing = mode == "disabled", mode == "missing"
				drift := resource.ReadResponse{State: adopt.State}
				r.Read(ctx, resource.ReadRequest{State: adopt.State}, &drift)
				if !drift.Diagnostics.HasError() {
					t.Fatal("accepted invalid key")
				}
			}
			disabled, missing, forbid = false, false, false
			original := payload
			payload = strings.ReplaceAll(payload, email, "other@example.iam.gserviceaccount.com")
			wrongAccount := create()
			if !wrongAccount.Diagnostics.HasError() || issued != 1 {
				t.Fatal("adopted payload for wrong account or reissued key")
			}
			payload = original
			disabled, missing, forbid, published = false, false, false, false
			empty := create()
			if !empty.Diagnostics.HasError() || issued != 1 {
				t.Fatal("empty reservation reissued")
			}
			deleted := resource.DeleteResponse{State: adopt.State}
			r.Delete(ctx, resource.DeleteRequest{}, &deleted)
			if !deleted.State.Raw.IsNull() || !reserved {
				t.Fatal("destroy retention failed")
			}
		})
	}
}
