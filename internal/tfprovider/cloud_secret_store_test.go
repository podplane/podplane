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
	value       []byte
	stringValue *string
	version     string
	described   map[string][]string
	getCalls    int
}

// CreateSecret returns the configured creation result.
func (f *fakeSecrets) CreateSecret(context.Context, *awssecrets.CreateSecretInput, ...func(*awssecrets.Options)) (*awssecrets.CreateSecretOutput, error) {
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
	key := []byte("winner")
	created := &fakeSecrets{version: "v1"}
	store := &awsSecretsStore{client: created, name: "/p/test-secret", identity: "id"}
	meta, winner, err := store.CreateOrAdopt(context.Background(), key)
	if err != nil || meta.Version != "v1" || created.getCalls != 0 || !bytes.Equal(winner, key) {
		t.Fatalf("create = %+v, calls %d, %v", meta, created.getCalls, err)
	}
	other := []byte("loser")
	adopted := &fakeSecrets{createErr: &secretstypes.ResourceExistsException{}, value: key, version: "winner"}
	store.client = adopted
	meta, winner, err = store.CreateOrAdopt(context.Background(), other)
	if err != nil || meta.Version != "winner" || adopted.getCalls != 1 || !bytes.Equal(winner, key) {
		t.Fatalf("adopt = %+v, calls %d, %v", meta, adopted.getCalls, err)
	}
}

// TestAWSSecretsRejectsStringOrEmptyAdoption verifies only binary keys are adopted.
func TestAWSSecretsRejectsStringOrEmptyAdoption(t *testing.T) {
	for _, fake := range []*fakeSecrets{
		{createErr: &secretstypes.ResourceExistsException{}, version: "v1"},
		{createErr: &secretstypes.ResourceExistsException{}, stringValue: aws.String("pem"), version: "v1"},
	} {
		store := &awsSecretsStore{client: fake, name: "/p/test-secret", identity: "id"}
		if _, _, err := store.CreateOrAdopt(context.Background(), []byte("new")); err == nil {
			t.Fatal("accepted an empty AWS Secrets Manager value")
		}
	}
}

// fakeSSM returns configured Parameter Store metadata.
type fakeSSM struct{ describe *ssm.DescribeParametersOutput }

// PutParameter is unused by metadata tests.
func (*fakeSSM) PutParameter(context.Context, *ssm.PutParameterInput, ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	return nil, errors.New("not used")
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
	addErr                error
	secret                *secretmanagerpb.Secret
	winner                *secretmanagerpb.AccessSecretVersionResponse
	addCalls, accessCalls int
}

// CreateSecret returns the configured secret creation result.
func (f *fakeGCPSecrets) CreateSecret(context.Context, *secretmanagerpb.CreateSecretRequest, ...gax.CallOption) (*secretmanagerpb.Secret, error) {
	if f.secret == nil {
		f.secret = &secretmanagerpb.Secret{Name: "projects/p/secrets/test-secret", Etag: "e1"}
	}
	return f.secret, f.createErr
}

// AddSecretVersion returns the configured version creation result.
func (f *fakeGCPSecrets) AddSecretVersion(context.Context, *secretmanagerpb.AddSecretVersionRequest, ...gax.CallOption) (*secretmanagerpb.SecretVersion, error) {
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
