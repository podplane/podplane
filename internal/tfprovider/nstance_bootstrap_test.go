// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"cloud.google.com/go/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	awssecrets "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	secretstypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/googleapis/gax-go/v2"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/netsy-dev/netsy/pkg/datafile"
	"github.com/nstance-dev/nstance/v2/pkg/nonce"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// registrationSSM implements create-only secure storage and pinned reads.
type registrationSSM struct {
	value []byte
	name  string
}

// PutParameter stores one SecureString and rejects overwrites or unsafe parameter types.
func (f *registrationSSM) PutParameter(_ context.Context, in *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	if aws.ToBool(in.Overwrite) || in.Type != ssmtypes.ParameterTypeSecureString {
		return nil, errors.New("unsafe parameter write")
	}
	if f.value != nil {
		return nil, &ssmtypes.ParameterAlreadyExists{}
	}
	f.name, f.value = aws.ToString(in.Name), []byte(aws.ToString(in.Value))
	return &ssm.PutParameterOutput{Version: 3}, nil
}

// GetParameter returns the stored key only for its current or pinned name with decryption enabled.
func (f *registrationSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if !aws.ToBool(in.WithDecryption) || aws.ToString(in.Name) != f.name && aws.ToString(in.Name) != f.name+":3" {
		return nil, errors.New("wrong key version or decryption")
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Name: aws.String(f.name), Type: ssmtypes.ParameterTypeSecureString, Value: aws.String(string(f.value)), Version: 3}}, nil
}

// DescribeParameters returns the stored parameter's metadata without its value.
func (f *registrationSSM) DescribeParameters(context.Context, *ssm.DescribeParametersInput, ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error) {
	return &ssm.DescribeParametersOutput{Parameters: []ssmtypes.ParameterMetadata{{Name: aws.String(f.name), Type: ssmtypes.ParameterTypeSecureString, Version: 3}}}, nil
}

// registrationSecrets emulates a single immutable AWS Secrets Manager signing key.
type registrationSecrets struct{ value []byte }

// CreateSecret stores the initial binary key and rejects subsequent creation attempts.
func (f *registrationSecrets) CreateSecret(_ context.Context, in *awssecrets.CreateSecretInput, _ ...func(*awssecrets.Options)) (*awssecrets.CreateSecretOutput, error) {
	if f.value != nil {
		return nil, &secretstypes.ResourceExistsException{}
	}
	f.value = bytes.Clone(in.SecretBinary)
	return &awssecrets.CreateSecretOutput{VersionId: aws.String("version-5")}, nil
}

// GetSecretValue returns the stored key and rejects an incorrect pinned version.
func (f *registrationSecrets) GetSecretValue(_ context.Context, in *awssecrets.GetSecretValueInput, _ ...func(*awssecrets.Options)) (*awssecrets.GetSecretValueOutput, error) {
	if in.VersionId != nil && aws.ToString(in.VersionId) != "version-5" {
		return nil, errors.New("wrong version")
	}
	return &awssecrets.GetSecretValueOutput{SecretBinary: bytes.Clone(f.value), VersionId: aws.String("version-5")}, nil
}

// DescribeSecret reports the fixed current version without exposing key bytes.
func (*registrationSecrets) DescribeSecret(context.Context, *awssecrets.DescribeSecretInput, ...func(*awssecrets.Options)) (*awssecrets.DescribeSecretOutput, error) {
	return &awssecrets.DescribeSecretOutput{VersionIdsToStages: map[string][]string{"version-5": {"AWSCURRENT"}}}, nil
}

// registrationGCP emulates a Google secret with a single immutable signing-key version.
type registrationGCP struct {
	fakeGCPSecrets
	value []byte
	name  string
}

// CreateSecret records the secret name and rejects duplicate creation.
func (f *registrationGCP) CreateSecret(_ context.Context, in *secretmanagerpb.CreateSecretRequest, _ ...gax.CallOption) (*secretmanagerpb.Secret, error) {
	if f.name != "" {
		return nil, status.Error(codes.AlreadyExists, "exists")
	}
	f.name = in.Parent + "/secrets/" + in.SecretId
	return &secretmanagerpb.Secret{Name: f.name}, nil
}

// AddSecretVersion stores the initial key and rejects any attempt to rotate it.
func (f *registrationGCP) AddSecretVersion(_ context.Context, in *secretmanagerpb.AddSecretVersionRequest, _ ...gax.CallOption) (*secretmanagerpb.SecretVersion, error) {
	if f.value != nil {
		return nil, errors.New("unexpected rotation")
	}
	f.value = bytes.Clone(in.Payload.Data)
	return &secretmanagerpb.SecretVersion{Name: f.name + "/versions/7"}, nil
}

// AccessSecretVersion returns the key for the latest alias or its fixed version name.
func (f *registrationGCP) AccessSecretVersion(_ context.Context, in *secretmanagerpb.AccessSecretVersionRequest, _ ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error) {
	if in.Name != f.name+"/versions/latest" && in.Name != f.name+"/versions/7" {
		return nil, errors.New("wrong key version")
	}
	return &secretmanagerpb.AccessSecretVersionResponse{Name: f.name + "/versions/7", Payload: &secretmanagerpb.SecretPayload{Data: bytes.Clone(f.value)}}, nil
}

// GetSecretVersion reports an enabled version without reading its payload.
func (f *registrationGCP) GetSecretVersion(context.Context, *secretmanagerpb.GetSecretVersionRequest, ...gax.CallOption) (*secretmanagerpb.SecretVersion, error) {
	return &secretmanagerpb.SecretVersion{Name: f.name + "/versions/7", State: secretmanagerpb.SecretVersion_ENABLED}, nil
}

// registrationStore builds an in-memory signing-key backend with cloud-specific naming.
func registrationStore(t *testing.T, provider string) (cloudSecretStore, cloudSecretOptions) {
	t.Helper()
	prefix, project := "/nstance/test/", ""
	if provider == cloudSecretProviderGCPSecretManager {
		prefix, project = "test_", "test-project"
	}
	opts, err := nstanceSigningSecret(provider, prefix, "", "", project)
	if err != nil {
		t.Fatal(err)
	}
	if provider == cloudSecretProviderGCPSecretManager {
		return &gcpSecretStore{client: &registrationGCP{}, parent: "projects/" + project, secretID: prefix + "registration-nonce-key", name: opts.Name, identity: opts.Identity()}, opts
	}
	if provider == cloudSecretProviderAWSSecretsManager {
		return &awsSecretsStore{client: &registrationSecrets{}, name: opts.Name, identity: opts.Identity()}, opts
	}
	return &awsSSMStore{client: &registrationSSM{}, name: opts.Name, identity: opts.Identity()}, opts
}

// TestRegistrationKeyCreateAdoptAndPinnedRead verifies stable key adoption and version-pinned reads on every backend.
func TestRegistrationKeyCreateAdoptAndPinnedRead(t *testing.T) {
	for _, provider := range []string{cloudSecretProviderAWSSSM, cloudSecretProviderAWSSecretsManager, cloudSecretProviderGCPSecretManager} {
		t.Run(provider, func(t *testing.T) {
			store, opts := registrationStore(t, provider)
			first, hash, err := createNstanceSigningKey(context.Background(), store)
			if err != nil || first.Identity != opts.Identity() || len(hash) != 64 {
				t.Fatalf("create metadata = %+v, %v", first, err)
			}
			// A lost Terraform state write and a concurrent creator take the same
			// adoption path: the newly generated loser must not replace the winner.
			second, adoptedHash, err := createNstanceSigningKey(context.Background(), store)
			if err != nil || first != second || hash != adoptedHash {
				t.Fatalf("adopt changed durable key: %v", err)
			}
			value, err := cloudSecretValue(context.Background(), store, first.Version)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(value)
			gotHash, err := workloadCAKeyFingerprint(value)
			if err != nil || gotHash != hash {
				t.Fatal("pinned value differs from adopted key")
			}
			if _, err := cloudSecretValue(context.Background(), store, "wrong-version"); err == nil {
				t.Fatal("accepted an unpinned key version")
			}
		})
	}
}

// TestRegistrationKeyRejectsNstanceIncompatiblePEM rejects trailing data during key adoption.
func TestRegistrationKeyRejectsNstanceIncompatiblePEM(t *testing.T) {
	store, opts := registrationStore(t, cloudSecretProviderAWSSSM)
	key, err := generateWorkloadCAKey()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	backend := store.(*awsSSMStore).client.(*registrationSSM)
	backend.name, backend.value = opts.Name, append(bytes.Clone(key), '\n')
	if _, _, err := createNstanceSigningKey(context.Background(), store); err == nil {
		t.Fatal("adopted PEM trailing data that Nstance's parser rejects")
	}
}

// snapshotCloud emulates only the S3/GCS wire operations needed by seed Create.
// Actual cloud SDKs handle requests, metadata, uploads, and native preconditions.
type snapshotCloud struct {
	mu           sync.Mutex
	provider     string
	payload      []byte
	completion   string
	ca           []byte
	live         bool
	loseResponse bool
	caDelayed    bool
}

// ServeHTTP emulates snapshot metadata, create-only uploads, prefix listings, and public CA reads.
func (f *snapshotCloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasSuffix(r.URL.Path, "/ca.crt") {
		if f.caDelayed {
			f.caDelayed = false
			w.WriteHeader(404)
			if f.provider == "s3" {
				_, _ = io.WriteString(w, "<Error><Code>NoSuchKey</Code></Error>")
			}
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Content-Length", fmt.Sprint(len(f.ca)))
		_, _ = w.Write(f.ca)
		return
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		if f.payload != nil || f.live {
			w.WriteHeader(412)
			return
		}
		if f.provider == "s3" {
			if r.Header.Get("If-None-Match") != "*" || r.Header.Get("X-Amz-Server-Side-Encryption") != "AES256" {
				w.WriteHeader(400)
				return
			}
			f.completion = r.Header.Get("X-Amz-Meta-Podplane-Seed")
			f.payload, _ = io.ReadAll(r.Body)
		} else {
			if r.URL.Query().Get("ifGenerationMatch") != "0" {
				w.WriteHeader(400)
				return
			}
			_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			parts := multipart.NewReader(r.Body, params["boundary"])
			part, err := parts.NextPart()
			if err != nil {
				w.WriteHeader(400)
				return
			}
			var metadata struct {
				Metadata map[string]string `json:"metadata"`
			}
			if json.NewDecoder(part).Decode(&metadata) != nil {
				w.WriteHeader(400)
				return
			}
			f.completion = metadata.Metadata[seedCompletionMetadata]
			part, err = parts.NextPart()
			if err != nil {
				w.WriteHeader(400)
				return
			}
			f.payload, _ = io.ReadAll(part)
		}
		if f.loseResponse {
			f.loseResponse = false
			w.WriteHeader(412)
			return
		}
		if f.provider == "gcs" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"bucket":"seed-bucket","name":"netsy/bootstrap.netsy","generation":"9"}`)
		}
		return
	}
	if r.Method == http.MethodHead || strings.Contains(r.URL.Path, "/o/") {
		if f.payload == nil {
			w.WriteHeader(404)
			return
		}
		if f.provider == "s3" {
			w.Header().Set("ETag", `"seed-etag"`)
			w.Header().Set("X-Amz-Server-Side-Encryption", "AES256")
			w.Header().Set("X-Amz-Meta-Podplane-Seed", f.completion)
		} else {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"bucket": "seed-bucket", "name": "netsy/bootstrap.netsy", "generation": "9", "metadata": map[string]string{seedCompletionMetadata: f.completion}})
		}
		return
	}
	if f.provider == "s3" {
		count := 0
		if f.live || f.payload != nil {
			count = 1
		}
		_, _ = fmt.Fprintf(w, `<ListBucketResult><KeyCount>%d</KeyCount></ListBucketResult>`, count)
	} else {
		w.Header().Set("Content-Type", "application/json")
		if f.live || f.payload != nil {
			_, _ = io.WriteString(w, `{"items":[{"name":"netsy/live-state"}]}`)
		} else {
			_, _ = io.WriteString(w, `{"items":[]}`)
		}
	}
}

// cloudSeedStore connects a real cloud SDK to the TLS snapshot emulator.
func cloudSeedStore(t *testing.T, provider string, opts SeedOptions, cloud *snapshotCloud) seedSnapshotStore {
	t.Helper()
	server := httptest.NewTLSServer(cloud)
	t.Cleanup(server.Close)
	if provider == "s3" {
		client := s3.New(s3.Options{Region: "us-east-1", BaseEndpoint: aws.String(server.URL), UsePathStyle: true, HTTPClient: server.Client(), Credentials: credentials.NewStaticCredentialsProvider("test", "test", "")})
		return &s3SeedStore{client: client, opts: opts}
	}
	client, err := storage.NewClient(context.Background(), option.WithEndpoint(server.URL+"/storage/v1/"), option.WithHTTPClient(server.Client()), option.WithoutAuthentication(), storage.WithJSONReads())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return &gcsSeedStore{client: client, opts: opts}
}

// bootstrapCA generates a short-lived self-signed CA certificate for bootstrap tests.
func bootstrapCA(t *testing.T) []byte {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(12), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// bootstrapOptions creates signer metadata, temporary seed inputs, and operator bootstrap settings.
func bootstrapOptions(t *testing.T, store cloudSecretStore, secret cloudSecretOptions) SeedOptions {
	t.Helper()
	meta, hash, err := createNstanceSigningKey(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	b := &nstanceBootstrapModel{Provider: types.StringValue(secret.Provider), SecretPrefix: types.StringValue(strings.TrimSuffix(strings.TrimPrefix(secret.Name, "projects/"+secret.Project+"/secrets/"), "registration-nonce-key")), SigningKeyID: types.StringValue(meta.Identity), SigningKeyVersion: types.StringValue(meta.Version), SigningKeyFingerprint: types.StringValue(hash), ClusterID: types.StringValue("testcluster"), Tenant: types.StringValue("tenant-west"), Namespace: types.StringValue("platform-nstance"), CABucket: types.StringValue("ca-bucket"), CAPrefix: types.StringValue("cluster"), CAConfigMap: types.StringValue("nstance-cluster-ca"), NonceSecret: types.StringValue("nstance-operator-nonce")}
	if secret.Provider == cloudSecretProviderAWSSSM {
		b.SecretPrefix = types.StringValue(strings.TrimSuffix(secret.Name, "registration-nonce.key"))
	}
	var seed bytes.Buffer
	if err := datafile.WriteSnapshot(&seed, []*datafile.Record{{Revision: 4, CreateRevision: 4, Version: 1, Created: true, Key: []byte("/registry/helm.toolkit.fluxcd.io/helmreleases/platform-components/platform-components"), Value: []byte(`{"apiVersion":"helm.toolkit.fluxcd.io/v2","kind":"HelmRelease","metadata":{"name":"platform-components","namespace":"platform-components"},"spec":{"values":{}}}`)}}, "generic"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	seedPath := filepath.Join(dir, "generic.netsy")
	if err := os.WriteFile(seedPath, seed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha512.Sum512(seed.Bytes())
	configPath := filepath.Join(dir, "cluster.jsonc")
	provider := "aws"
	secretsProvider := `{"kind":"aws","object_type":"ssmparameter"}`
	if secret.Project != "" {
		provider = "google"
		secretsProvider = fmt.Sprintf(`{"kind":"gcp","project_id":%q}`, secret.Project)
	}
	config := fmt.Sprintf(`{"cluster":{"id":"testcluster","oidc":{"issuer_url":"https://oidc.example.com"},"providers":[{"kind":%q}],"secrets":{"default_provider":"default","providers":{"default":%s}},"seed":{"name":"recommended","version":"v1.0.0","digest":"sha512:%s"}}}`, provider, secretsProvider, hex.EncodeToString(sum[:]))
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	return SeedOptions{ClusterConfigPath: configPath, SeedPath: seedPath, Bucket: "seed-bucket", Prefix: "netsy", Project: secret.Project, NstanceBootstrap: b, ValuesContent: `{"platform":{"components":{"apps":{"nstance-operator":{"enabled":true}}}}}`}
}

// TestSeedBootstrapCloudCreateAndRecovery checks nonce contents, concurrent creation, and metadata-only recovery.
func TestSeedBootstrapCloudCreateAndRecovery(t *testing.T) {
	for _, provider := range []string{"s3", "gcs"} {
		t.Run(provider, func(t *testing.T) {
			secretProvider := cloudSecretProviderAWSSSM
			if provider == "gcs" {
				secretProvider = cloudSecretProviderGCPSecretManager
			}
			keyStore, secret := registrationStore(t, secretProvider)
			opts := bootstrapOptions(t, keyStore, secret)
			scratch := t.TempDir()
			t.Setenv("TMPDIR", scratch)
			cloud := &snapshotCloud{provider: provider, ca: bootstrapCA(t), loseResponse: true}
			store := cloudSeedStore(t, provider, opts, cloud)
			results := make(chan seedSnapshotMetadata, 2)
			failures := make(chan error, 2)
			for range 2 {
				go func() {
					meta, err := createSeed(context.Background(), opts, store, func(context.Context, cloudSecretOptions) (cloudSecretStore, error) { return keyStore, nil })
					results <- meta
					failures <- err
				}()
			}
			meta, other := <-results, <-results
			for range 2 {
				if err := <-failures; err != nil {
					t.Fatalf("concurrent create failed: %v", err)
				}
			}
			expectedVersion := `"seed-etag"`
			if provider == "gcs" {
				expectedVersion = "9"
			}
			if meta.Version != expectedVersion || !meta.Encrypted || meta != other {
				t.Fatalf("concurrent creators did not adopt the same encrypted snapshot: %+v / %+v", meta, other)
			}
			records, err := datafile.ReadSnapshot(bytes.NewReader(cloud.payload))
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 4 {
				t.Fatalf("records = %d, want 4", len(records))
			}
			values := map[string][]byte{}
			for i, record := range records {
				values[string(record.Key)] = record.Value
				if record.Revision != int64(i+1) || record.CreateRevision != record.Revision {
					t.Fatal("non-contiguous bootstrap revisions")
				}
			}
			var configMap struct {
				Data map[string]string `json:"data"`
			}
			if json.Unmarshal(values["/registry/configmaps/platform-nstance/nstance-cluster-ca"], &configMap) != nil || configMap.Data["ca.crt"] != string(cloud.ca) {
				t.Fatal("incorrect public CA ConfigMap")
			}
			var nonceSecret struct {
				Data map[string][]byte `json:"data"`
			}
			if json.Unmarshal(values["/registry/secrets/platform-nstance/nstance-operator-nonce"], &nonceSecret) != nil {
				t.Fatal("invalid bootstrap Secret")
			}
			value, err := cloudSecretValue(context.Background(), keyStore, opts.NstanceBootstrap.SigningKeyVersion.ValueString())
			if err != nil {
				t.Fatal(err)
			}
			defer clear(value)
			block, _ := pem.Decode(value)
			parsed, _ := x509.ParsePKCS8PrivateKey(block.Bytes)
			token := string(nonceSecret.Data["nonce.jwt"])
			claims, err := nonce.Validate(token, parsed.(ed25519.PrivateKey).Public().(ed25519.PublicKey), "operator")
			if err != nil || claims.Subject != "testcluster" || claims.ClusterID != "testcluster" || claims.Tenant != "tenant-west" || claims.ExpiresAt.Sub(claims.IssuedAt.Time) != 30*time.Minute || claims.ID == "" || claims.Shard != "" || claims.Group != "" {
				t.Fatalf("incorrect nonce claims: %v", err)
			}
			for key, record := range values {
				if bytes.Contains(record, []byte("PRIVATE KEY")) || bytes.Contains(record, []byte(base64.StdEncoding.EncodeToString(value))) {
					t.Fatal("snapshot contains the private signing key")
				}
				if key != "/registry/secrets/platform-nstance/nstance-operator-nonce" && (bytes.Contains(record, []byte(token)) || bytes.Contains(record, []byte(base64.StdEncoding.EncodeToString([]byte(token))))) {
					t.Fatalf("bootstrap token leaked outside its Secret into %s", key)
				}
			}
			if _, err := nonce.Validate(token, parsed.(ed25519.PrivateKey).Public().(ed25519.PublicKey), "agent"); err == nil {
				t.Fatal("operator nonce accepted as agent credential")
			}
			if _, ok := values["/registry/namespaces/platform-nstance"]; !ok {
				t.Fatal("missing operator namespace")
			}
			// Upload-before-state interruption: remove local source files and make
			// the key backend unavailable. Adoption must still succeed unchanged.
			before := bytes.Clone(cloud.payload)
			if err := os.Remove(opts.SeedPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(opts.ClusterConfigPath); err != nil {
				t.Fatal(err)
			}
			cloud.live = true
			recovered, err := createSeed(context.Background(), opts, store, func(context.Context, cloudSecretOptions) (cloudSecretStore, error) {
				return nil, errors.New("key backend unavailable")
			})
			if err != nil || recovered != meta || !bytes.Equal(before, cloud.payload) {
				t.Fatalf("recovery changed seed: %v", err)
			}
			changed := *opts.NstanceBootstrap
			changed.Tenant = types.StringValue("other-tenant")
			opts.NstanceBootstrap = &changed
			if _, err := createSeed(context.Background(), opts, store, newCloudSecretStore); err == nil {
				t.Fatal("adopted another tenant's bootstrap")
			}
			files, err := os.ReadDir(scratch)
			if err != nil || len(files) != 0 {
				t.Fatal("credential-bearing snapshot generation created temporary files")
			}
		})
	}
}

// TestPublicCADelayedAndBounded verifies delayed CA publication is retried and waiting respects the deadline.
func TestPublicCADelayedAndBounded(t *testing.T) {
	for _, provider := range []string{"s3", "gcs"} {
		t.Run(provider, func(t *testing.T) {
			cloud := &snapshotCloud{provider: provider, ca: bootstrapCA(t), caDelayed: true}
			store := cloudSeedStore(t, provider, SeedOptions{Bucket: "seed-bucket"}, cloud)
			b := &nstanceBootstrapModel{CABucket: types.StringValue("ca-bucket"), CAPrefix: types.StringValue("cluster")}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			ca, err := waitPublicCA(ctx, func(ctx context.Context) ([]byte, error) { return store.PublicCA(ctx, b) }, time.Millisecond)
			if err != nil || !bytes.Equal(ca, cloud.ca) {
				t.Fatalf("delayed CA = %v", err)
			}
			ctx, cancel = context.WithTimeout(context.Background(), 5*time.Millisecond)
			defer cancel()
			if _, err := waitPublicCA(ctx, func(context.Context) ([]byte, error) { return nil, errSnapshotAbsent }, time.Millisecond); err == nil {
				t.Fatal("CA wait did not time out")
			}
		})
	}
}

// TestSeedRejectsLiveStateAndDisabledOverlay checks bootstrap guards and credential-free seeding without bootstrap.
func TestSeedRejectsLiveStateAndDisabledOverlay(t *testing.T) {
	for _, provider := range []string{"s3", "gcs"} {
		t.Run(provider, func(t *testing.T) {
			secretProvider := cloudSecretProviderAWSSSM
			if provider == "gcs" {
				secretProvider = cloudSecretProviderGCPSecretManager
			}
			keyStore, secret := registrationStore(t, secretProvider)
			opts := bootstrapOptions(t, keyStore, secret)
			cloud := &snapshotCloud{provider: provider, live: true}
			store := cloudSeedStore(t, provider, opts, cloud)
			if _, err := createSeed(context.Background(), opts, store, newCloudSecretStore); err == nil || cloud.payload != nil {
				t.Fatal("seed overwrote live Netsy state")
			}
			cloud.live = false
			opts.ValuesContent = `{"platform":{"components":{"apps":{"nstance-operator":{"enabled":false}}}}}`
			if _, err := createSeed(context.Background(), opts, store, newCloudSecretStore); err == nil || !strings.Contains(err.Error(), "overlay is disabled") || cloud.payload != nil {
				t.Fatalf("disabled overlay: %v", err)
			}
			// Without bootstrap configuration, even the recommended seed is a
			// credential-free snapshot and has no dependency on a key backend.
			opts.NstanceBootstrap = nil
			scratch := t.TempDir()
			t.Setenv("TMPDIR", scratch)
			if _, err := createSeed(context.Background(), opts, store, func(context.Context, cloudSecretOptions) (cloudSecretStore, error) {
				return nil, errors.New("no signing key configured")
			}); err != nil {
				t.Fatal(err)
			}
			records, err := datafile.ReadSnapshot(bytes.NewReader(cloud.payload))
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 || string(records[0].Key) != "/registry/helm.toolkit.fluxcd.io/helmreleases/platform-components/platform-components" {
				t.Fatal("snapshot without bootstrap must contain only the HelmRelease")
			}
			if bytes.Contains(records[0].Value, []byte("PRIVATE KEY")) {
				t.Fatal("generic snapshot contains a private key")
			}
			files, err := os.ReadDir(scratch)
			if err != nil || len(files) != 0 {
				t.Fatal("cluster-specific snapshot generation created temporary files")
			}
		})
	}
}
