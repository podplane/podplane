// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"context"
	"errors"
	"testing"

	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/aws/aws-sdk-go-v2/aws"
	awssecrets "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	secretstypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeSecrets records AWS Secrets Manager calls for backend tests.
type fakeSecrets struct {
	createErr   error
	created     *awssecrets.CreateSecretInput
	value       []byte
	stringValue *string
	version     string
	described   map[string][]string
	getCalls    int
}

// CreateSecret returns the configured creation result.
func (f *fakeSecrets) CreateSecret(_ context.Context, input *awssecrets.CreateSecretInput, _ ...func(*awssecrets.Options)) (*awssecrets.CreateSecretOutput, error) {
	f.created = input
	return &awssecrets.CreateSecretOutput{VersionId: aws.String(f.version)}, f.createErr
}

// GetSecretValue returns the configured current secret value.
func (f *fakeSecrets) GetSecretValue(context.Context, *awssecrets.GetSecretValueInput, ...func(*awssecrets.Options)) (*awssecrets.GetSecretValueOutput, error) {
	f.getCalls++
	return &awssecrets.GetSecretValueOutput{SecretBinary: f.value, SecretString: f.stringValue, VersionId: aws.String(f.version)}, nil
}

// DescribeSecret returns the configured secret version metadata.
func (f *fakeSecrets) DescribeSecret(context.Context, *awssecrets.DescribeSecretInput, ...func(*awssecrets.Options)) (*awssecrets.DescribeSecretOutput, error) {
	return &awssecrets.DescribeSecretOutput{VersionIdsToStages: f.described}, nil
}

// TestAWSSecretsCreateAndConcurrentAdopt verifies create-only and adoption paths.
func TestAWSSecretsCreateAndConcurrentAdopt(t *testing.T) {
	key := []byte("  café �\r\nYWJjZA==\n")
	created := &fakeSecrets{version: "v1"}
	store := &awsSecretsStore{client: created, name: "/p/test-secret", identity: "id", tags: map[string]string{"podplane.dev/binding": "sourcegateway"}}
	meta, winner, err := store.CreateOrAdopt(context.Background(), key)
	if err != nil || meta.Version != "v1" || created.getCalls != 0 || !bytes.Equal(winner, key) {
		t.Fatalf("create = %+v, calls %d, %v", meta, created.getCalls, err)
	}
	if created.created.SecretBinary != nil || aws.ToString(created.created.SecretString) != string(key) {
		t.Fatal("creation must preserve text in SecretString")
	}
	if len(created.created.Tags) != 1 || aws.ToString(created.created.Tags[0].Key) != "podplane.dev/binding" || aws.ToString(created.created.Tags[0].Value) != "sourcegateway" {
		t.Fatal("creation did not preserve discovery tags")
	}
	other := []byte("loser")
	adopted := &fakeSecrets{createErr: &secretstypes.ResourceExistsException{}, stringValue: aws.String(string(key)), version: "winner"}
	store.client = adopted
	meta, winner, err = store.CreateOrAdopt(context.Background(), other)
	if err != nil || meta.Version != "winner" || adopted.getCalls != 1 || !bytes.Equal(winner, key) {
		t.Fatalf("adopt = %+v, calls %d, %v", meta, adopted.getCalls, err)
	}
	pinned, err := cloudSecretValue(context.Background(), store, meta.Version)
	if err != nil || !bytes.Equal(pinned, key) {
		t.Fatalf("pinned read changed adopted text: %v", err)
	}
}

// TestAWSSecretsRequiresText verifies adoption and pinned reads reject empty or binary values.
func TestAWSSecretsRequiresText(t *testing.T) {
	for _, fake := range []*fakeSecrets{
		{createErr: &secretstypes.ResourceExistsException{}, version: "v1"},
		{createErr: &secretstypes.ResourceExistsException{}, stringValue: aws.String(""), version: "v1"},
		{createErr: &secretstypes.ResourceExistsException{}, value: []byte("valid text stored as binary"), version: "v1"},
	} {
		store := &awsSecretsStore{client: fake, name: "/p/test-secret", identity: "id"}
		if _, _, err := store.CreateOrAdopt(context.Background(), []byte("new")); err == nil {
			t.Fatal("adopted an empty or binary AWS Secrets Manager value")
		}
		if _, err := cloudSecretValue(context.Background(), store, "v1"); err == nil {
			t.Fatal("read an empty or binary AWS Secrets Manager value")
		}
	}
}

// fakeSSM records writes and returns configured Parameter Store metadata.
type fakeSSM struct {
	describe *ssm.DescribeParametersOutput
	created  *ssm.PutParameterInput
}

// PutParameter records a successful create.
func (f *fakeSSM) PutParameter(_ context.Context, input *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	f.created = input
	return &ssm.PutParameterOutput{Version: 1}, nil
}

// GetParameter is unused by metadata tests.
func (*fakeSSM) GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	return nil, errors.New("not used")
}

// DescribeParameters returns the configured parameter metadata.
func (f *fakeSSM) DescribeParameters(context.Context, *ssm.DescribeParametersInput, ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error) {
	return f.describe, nil
}

// TestSSMMetadataDoesNotReadValue verifies refresh uses only parameter metadata.
func TestSSMMetadataDoesNotReadValue(t *testing.T) {
	store := &awsSSMStore{client: &fakeSSM{describe: &ssm.DescribeParametersOutput{Parameters: []ssmtypes.ParameterMetadata{{Name: aws.String("/p/test-secret"), Version: 7, Type: ssmtypes.ParameterTypeSecureString}}}}, name: "/p/test-secret", identity: "id"}
	meta, err := store.Metadata(context.Background())
	if err != nil || meta.Version != "7" {
		t.Fatalf("metadata = %+v, %v", meta, err)
	}
}

// TestSSMMetadataRejectsNonSecureString verifies plain parameters cannot be adopted.
func TestSSMMetadataRejectsNonSecureString(t *testing.T) {
	store := &awsSSMStore{client: &fakeSSM{describe: &ssm.DescribeParametersOutput{Parameters: []ssmtypes.ParameterMetadata{{Name: aws.String("/p/test-secret"), Version: 7, Type: ssmtypes.ParameterTypeString}}}}, name: "/p/test-secret", identity: "id"}
	if _, err := store.Metadata(context.Background()); err == nil {
		t.Fatal("accepted a non-SecureString SSM parameter")
	}
}

// fakeGCPSecrets records Google Secret Manager calls for backend tests.
type fakeGCPSecrets struct {
	createErr             error
	created               *secretmanagerpb.CreateSecretRequest
	added                 *secretmanagerpb.AddSecretVersionRequest
	addErr                error
	secret                *secretmanagerpb.Secret
	winner                *secretmanagerpb.AccessSecretVersionResponse
	addCalls, accessCalls int
}

// CreateSecret returns the configured secret creation result.
func (f *fakeGCPSecrets) CreateSecret(_ context.Context, input *secretmanagerpb.CreateSecretRequest, _ ...gax.CallOption) (*secretmanagerpb.Secret, error) {
	f.created = input
	if f.secret == nil {
		f.secret = &secretmanagerpb.Secret{Name: "projects/p/secrets/test-secret", Etag: "e1"}
	}
	return f.secret, f.createErr
}

// AddSecretVersion returns the configured version creation result.
func (f *fakeGCPSecrets) AddSecretVersion(_ context.Context, input *secretmanagerpb.AddSecretVersionRequest, _ ...gax.CallOption) (*secretmanagerpb.SecretVersion, error) {
	f.added = input
	f.addCalls++
	return &secretmanagerpb.SecretVersion{Name: "projects/p/secrets/test-secret/versions/1"}, f.addErr
}

// AccessSecretVersion returns the configured current value.
func (f *fakeGCPSecrets) AccessSecretVersion(context.Context, *secretmanagerpb.AccessSecretVersionRequest, ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error) {
	f.accessCalls++
	if f.winner == nil {
		return nil, status.Error(codes.NotFound, "empty")
	}
	return f.winner, nil
}

// GetSecretVersion is unused by creation tests.
func (*fakeGCPSecrets) GetSecretVersion(context.Context, *secretmanagerpb.GetSecretVersionRequest, ...gax.CallOption) (*secretmanagerpb.SecretVersion, error) {
	return nil, errors.New("not used")
}

// Close releases resources held by the fake client.
func (*fakeGCPSecrets) Close() error { return nil }

// newTestGCPSecretStore returns a Google secret store backed by a fake client.
func newTestGCPSecretStore(client gcpSecretManagerAPI) *gcpSecretStore {
	return &gcpSecretStore{
		client:   client,
		parent:   "projects/p",
		secretID: "test-secret",
		name:     "projects/p/secrets/test-secret",
		identity: "id",
	}
}

// TestNewCloudSecretStoreRejectsMalformedName verifies invalid Google names are
// rejected before a cloud client is created.
func TestNewCloudSecretStoreRejectsMalformedName(t *testing.T) {
	_, err := newCloudSecretStore(context.Background(), cloudSecretOptions{Provider: cloudSecretProviderGCPSecretManager, Name: "test-secret"})
	if err == nil {
		t.Fatal("accepted malformed Google Secret Manager name")
	}
}

// TestCloudSecretText verifies new values remain text in each native format and
// invalid UTF-8 cannot create even an empty backend object.
func TestCloudSecretText(t *testing.T) {
	for _, value := range []string{"  café �\r\nYWJjZA==\n", "private\xffvalue", "private\xc3"} {
		sm := &fakeSecrets{version: "v1"}
		ssmClient := &fakeSSM{}
		gcp := &fakeGCPSecrets{}
		stores := []cloudSecretStore{
			&awsSecretsStore{client: sm, name: "/p/test-secret"},
			&awsSSMStore{client: ssmClient, name: "/p/test-secret"},
			newTestGCPSecretStore(gcp),
		}
		invalid := value == "private\xffvalue" || value == "private\xc3"
		for _, store := range stores {
			_, got, err := store.CreateOrAdopt(context.Background(), []byte(value))
			if invalid {
				if err == nil || err.Error() != "secret value must be valid UTF-8 text" {
					t.Fatalf("%T invalid value error = %v", store, err)
				}
			} else if err != nil || string(got) != value {
				t.Fatalf("%T failed to preserve text: %v", store, err)
			}
		}
		if invalid {
			if sm.created != nil || ssmClient.created != nil || gcp.created != nil || gcp.addCalls != 0 {
				t.Fatal("invalid value reached a backend write")
			}
			continue
		}
		if sm.created.SecretBinary != nil || aws.ToString(sm.created.SecretString) != value {
			t.Fatal("Secrets Manager changed text or used SecretBinary")
		}
		if ssmClient.created.Type != ssmtypes.ParameterTypeSecureString || aws.ToBool(ssmClient.created.Overwrite) || aws.ToString(ssmClient.created.Value) != value {
			t.Fatal("SSM changed text or did not use create-only SecureString")
		}
		if gcp.added == nil || string(gcp.added.Payload.Data) != value {
			t.Fatal("GCP changed text")
		}
	}
}

// TestGCPRejectsInterruptedEmptySecretWithoutMutation verifies retries do not
// populate an existing empty secret.
func TestGCPRejectsInterruptedEmptySecretWithoutMutation(t *testing.T) {
	fake := &fakeGCPSecrets{createErr: status.Error(codes.AlreadyExists, "exists"), secret: &secretmanagerpb.Secret{Name: "projects/p/secrets/test-secret", Etag: "e1"}}
	store := newTestGCPSecretStore(fake)
	if _, _, err := store.CreateOrAdopt(context.Background(), []byte("new")); err == nil || fake.addCalls != 0 {
		t.Fatalf("empty secret error = %v, add calls = %d", err, fake.addCalls)
	}
}

// TestGCPConcurrentCreatorAdoptsWithoutAddingVersion verifies a concurrent
// winner is adopted without creating another version.
func TestGCPConcurrentCreatorAdoptsWithoutAddingVersion(t *testing.T) {
	key := []byte("winner")
	fake := &fakeGCPSecrets{createErr: status.Error(codes.AlreadyExists, "exists"), winner: &secretmanagerpb.AccessSecretVersionResponse{Name: "projects/p/secrets/test-secret/versions/7", Payload: &secretmanagerpb.SecretPayload{Data: key}}}
	store := newTestGCPSecretStore(fake)
	meta, winner, err := store.CreateOrAdopt(context.Background(), []byte("loser"))
	if err != nil || fake.addCalls != 0 || meta.Version != "projects/p/secrets/test-secret/versions/7" || !bytes.Equal(winner, key) {
		t.Fatalf("adopt = %+v, add calls %d, %v", meta, fake.addCalls, err)
	}
}

// TestGCPRejectsMalformedWinner verifies an empty current version is not adopted.
func TestGCPRejectsMalformedWinner(t *testing.T) {
	fake := &fakeGCPSecrets{createErr: status.Error(codes.AlreadyExists, "exists"), winner: &secretmanagerpb.AccessSecretVersionResponse{Name: "projects/p/secrets/test-secret/versions/7"}}
	store := newTestGCPSecretStore(fake)
	if _, _, err := store.CreateOrAdopt(context.Background(), []byte("loser")); err == nil {
		t.Fatal("adopted a GCP version without a payload")
	}
}

// TestGCPFailedInitialVersionLeavesEmptySecret verifies an interrupted first
// version cannot be retried automatically.
func TestGCPFailedInitialVersionLeavesEmptySecret(t *testing.T) {
	fake := &fakeGCPSecrets{addErr: errors.New("interrupted")}
	store := newTestGCPSecretStore(fake)
	if _, _, err := store.CreateOrAdopt(context.Background(), []byte("key")); err == nil || fake.addCalls != 1 {
		t.Fatalf("add failure = %v, add calls = %d", err, fake.addCalls)
	}
	fake.createErr = status.Error(codes.AlreadyExists, "exists")
	if _, _, err := store.CreateOrAdopt(context.Background(), []byte("replacement")); err == nil || fake.addCalls != 1 {
		t.Fatalf("retry error = %v, add calls = %d", err, fake.addCalls)
	}
}

// TestGCPAddResponseErrorAdoptsCommittedVersion verifies a committed version is
// recovered when its creation response is lost.
func TestGCPAddResponseErrorAdoptsCommittedVersion(t *testing.T) {
	key := []byte("winner")
	fake := &fakeGCPSecrets{
		addErr: errors.New("response lost"),
		winner: &secretmanagerpb.AccessSecretVersionResponse{
			Name:    "projects/p/secrets/test-secret/versions/1",
			Payload: &secretmanagerpb.SecretPayload{Data: key},
		},
	}
	store := newTestGCPSecretStore(fake)
	meta, winner, err := store.CreateOrAdopt(context.Background(), key)
	if err != nil || meta.Version != "projects/p/secrets/test-secret/versions/1" || !bytes.Equal(winner, key) || fake.addCalls != 1 || fake.accessCalls != 1 {
		t.Fatalf("adopt committed add = %+v, add calls = %d, access calls = %d, error = %v", meta, fake.addCalls, fake.accessCalls, err)
	}
}

// TestGCPRegionalSecret verifies regional creation omits global replication settings.
func TestGCPRegionalSecret(t *testing.T) {
	client := &fakeGCPSecrets{}
	store := newTestGCPSecretStore(client)
	store.parent = "projects/p/locations/us-west1"
	if _, _, err := store.CreateOrAdopt(context.Background(), []byte("key")); err != nil {
		t.Fatal(err)
	}
	if client.created.Parent != store.parent || client.created.Secret.Replication != nil {
		t.Fatal("regional secrets must use the location parent without a replication policy")
	}
}
