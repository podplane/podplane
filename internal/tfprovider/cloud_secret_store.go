// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awssecrets "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	secretstypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	cloudSecretProviderAWSSecretsManager = "aws-secrets-manager"
	cloudSecretProviderAWSSSM            = "aws-parameter-store"
	cloudSecretProviderGCPSecretManager  = "google-secret-manager"
)

// cloudSecretOptions identifies a secret in a supported durable backend.
type cloudSecretOptions struct {
	Provider  string
	Name      string
	Region    string
	Profile   string
	Project   string
	GCPLabels map[string]string
}

// Identity returns the stable identity stored in Terraform state.
func (opts cloudSecretOptions) Identity() string {
	switch opts.Provider {
	case cloudSecretProviderAWSSecretsManager:
		return "aws-secretsmanager:" + opts.Name
	case cloudSecretProviderAWSSSM:
		return "aws-ssm:" + opts.Name
	case cloudSecretProviderGCPSecretManager:
		return "gcp-secretmanager:" + opts.Name
	default:
		return ""
	}
}

// cloudSecretMetadata identifies an immutable backend secret version.
type cloudSecretMetadata struct {
	Identity string
	Version  string
}

// cloudSecretStore creates a durable secret once and reads its current metadata.
type cloudSecretStore interface {
	CreateOrAdopt(context.Context, []byte) (cloudSecretMetadata, []byte, error)
	Metadata(context.Context) (cloudSecretMetadata, error)
	Close() error
}

// cloudSecretValue reads only a pinned immutable version during seed Create.
// Errors deliberately omit backend responses, which can contain secret payloads.
func cloudSecretValue(ctx context.Context, store cloudSecretStore, version string) ([]byte, error) {
	switch s := store.(type) {
	case *awsSecretsStore:
		out, err := s.client.GetSecretValue(ctx, &awssecrets.GetSecretValueInput{SecretId: aws.String(s.name), VersionId: aws.String(version)})
		if err == nil && out != nil && aws.ToString(out.VersionId) == version {
			if out.SecretString != nil {
				return []byte(*out.SecretString), nil
			}
			if len(out.SecretBinary) > 0 {
				return out.SecretBinary, nil
			}
		}
	case *awsSSMStore:
		out, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(s.name + ":" + version), WithDecryption: aws.Bool(true)})
		if err == nil && out != nil && out.Parameter != nil && out.Parameter.Type == ssmtypes.ParameterTypeSecureString && strconv.FormatInt(out.Parameter.Version, 10) == version && out.Parameter.Value != nil {
			return []byte(*out.Parameter.Value), nil
		}
	case *gcpSecretStore:
		if !strings.HasPrefix(version, s.name+"/versions/") || strings.HasSuffix(version, "/latest") {
			break
		}
		out, err := s.client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{Name: version})
		if err == nil && out != nil && out.Name == version && out.Payload != nil && len(out.Payload.Data) > 0 {
			return out.Payload.Data, nil
		}
	}
	return nil, errors.New("cannot read the pinned signing-key version; check secret backend permissions and restore the original version")
}

// newCloudSecretStore configures the selected secret backend.
func newCloudSecretStore(ctx context.Context, opts cloudSecretOptions) (cloudSecretStore, error) {
	if opts.Name == "" {
		return nil, errors.New("secret name is required")
	}
	identity := opts.Identity()
	switch opts.Provider {
	case cloudSecretProviderAWSSecretsManager, cloudSecretProviderAWSSSM:
		loadOpts := []func(*config.LoadOptions) error{}
		if opts.Region != "" {
			loadOpts = append(loadOpts, config.WithRegion(opts.Region))
		}
		if opts.Profile != "" {
			loadOpts = append(loadOpts, config.WithSharedConfigProfile(opts.Profile))
		}
		cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
		if err != nil {
			return nil, fmt.Errorf("load AWS configuration: %w", err)
		}
		if opts.Provider == cloudSecretProviderAWSSecretsManager {
			return &awsSecretsStore{client: awssecrets.NewFromConfig(cfg), name: opts.Name, identity: identity}, nil
		}
		return &awsSSMStore{client: ssm.NewFromConfig(cfg), name: opts.Name, identity: identity}, nil
	case cloudSecretProviderGCPSecretManager:
		parts := strings.Split(opts.Name, "/")
		if len(parts) != 4 || parts[0] != "projects" || parts[1] == "" || parts[2] != "secrets" || parts[3] == "" {
			return nil, fmt.Errorf("invalid GCP Secret Manager name %q", opts.Name)
		}
		clientOpts := []option.ClientOption{}
		if opts.Project != "" {
			clientOpts = append(clientOpts, option.WithQuotaProject(opts.Project))
		}
		client, err := secretmanager.NewClient(ctx, clientOpts...)
		if err != nil {
			return nil, fmt.Errorf("create GCP Secret Manager client: %w", err)
		}
		return &gcpSecretStore{
			client:   client,
			parent:   strings.Join(parts[:2], "/"),
			secretID: parts[3],
			name:     opts.Name,
			identity: identity,
			labels:   opts.GCPLabels,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", opts.Provider)
	}
}

// awsSecretsAPI is the AWS Secrets Manager API used by the resource.
type awsSecretsAPI interface {
	CreateSecret(context.Context, *awssecrets.CreateSecretInput, ...func(*awssecrets.Options)) (*awssecrets.CreateSecretOutput, error)
	GetSecretValue(context.Context, *awssecrets.GetSecretValueInput, ...func(*awssecrets.Options)) (*awssecrets.GetSecretValueOutput, error)
	DescribeSecret(context.Context, *awssecrets.DescribeSecretInput, ...func(*awssecrets.Options)) (*awssecrets.DescribeSecretOutput, error)
}

// awsSecretsStore stores opaque values in AWS Secrets Manager.
type awsSecretsStore struct {
	client         awsSecretsAPI
	name, identity string
}

// Close releases resources held by the store.
func (s *awsSecretsStore) Close() error { return nil }

// CreateOrAdopt creates the AWS secret or adopts its current immutable version.
func (s *awsSecretsStore) CreateOrAdopt(ctx context.Context, value []byte) (cloudSecretMetadata, []byte, error) {
	out, err := s.client.CreateSecret(ctx, &awssecrets.CreateSecretInput{Name: aws.String(s.name), SecretBinary: value})
	if err == nil {
		if out == nil || aws.ToString(out.VersionId) == "" {
			return cloudSecretMetadata{}, nil, errors.New("create AWS secret: backend returned no version")
		}
		return cloudSecretMetadata{s.identity, aws.ToString(out.VersionId)}, value, nil
	}
	var exists *secretstypes.ResourceExistsException
	if !errors.As(err, &exists) {
		return cloudSecretMetadata{}, nil, fmt.Errorf("create AWS secret: %w", err)
	}
	winner, err := s.client.GetSecretValue(ctx, &awssecrets.GetSecretValueInput{SecretId: aws.String(s.name), VersionStage: aws.String("AWSCURRENT")})
	if err != nil {
		return cloudSecretMetadata{}, nil, fmt.Errorf("adopt existing AWS secret current version: %w", err)
	}
	if winner == nil || len(winner.SecretBinary) == 0 || winner.SecretString != nil || aws.ToString(winner.VersionId) == "" {
		return cloudSecretMetadata{}, nil, fmt.Errorf("adopt existing AWS secret current version: value must be non-empty binary data, not a string")
	}
	return cloudSecretMetadata{s.identity, aws.ToString(winner.VersionId)}, winner.SecretBinary, nil
}

// Metadata returns the current AWS secret version without reading its value.
func (s *awsSecretsStore) Metadata(ctx context.Context) (cloudSecretMetadata, error) {
	out, err := s.client.DescribeSecret(ctx, &awssecrets.DescribeSecretInput{SecretId: aws.String(s.name)})
	if err != nil {
		return cloudSecretMetadata{}, fmt.Errorf("read AWS secret metadata: %w", err)
	}
	if out == nil {
		return cloudSecretMetadata{}, errors.New("read AWS secret metadata: backend returned no secret")
	}
	for version, stages := range out.VersionIdsToStages {
		for _, stage := range stages {
			if stage == "AWSCURRENT" {
				return cloudSecretMetadata{s.identity, version}, nil
			}
		}
	}
	return cloudSecretMetadata{}, fmt.Errorf("AWS secret has no current version")
}

// awsSSMAPI is the AWS Systems Manager Parameter Store API used by the resource.
type awsSSMAPI interface {
	PutParameter(context.Context, *ssm.PutParameterInput, ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
	GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
	DescribeParameters(context.Context, *ssm.DescribeParametersInput, ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error)
}

// awsSSMStore stores opaque values in Parameter Store.
type awsSSMStore struct {
	client         awsSSMAPI
	name, identity string
}

// Close releases resources held by the store.
func (s *awsSSMStore) Close() error { return nil }

// CreateOrAdopt creates the secure parameter or adopts its current version.
func (s *awsSSMStore) CreateOrAdopt(ctx context.Context, value []byte) (cloudSecretMetadata, []byte, error) {
	out, err := s.client.PutParameter(ctx, &ssm.PutParameterInput{Name: aws.String(s.name), Type: ssmtypes.ParameterTypeSecureString, Value: aws.String(string(value)), Overwrite: aws.Bool(false)})
	if err == nil {
		if out == nil || out.Version < 1 {
			return cloudSecretMetadata{}, nil, errors.New("create AWS SSM parameter: backend returned no version")
		}
		return cloudSecretMetadata{s.identity, strconv.FormatInt(out.Version, 10)}, value, nil
	}
	var exists *ssmtypes.ParameterAlreadyExists
	if !errors.As(err, &exists) {
		return cloudSecretMetadata{}, nil, fmt.Errorf("create AWS SSM parameter: %w", err)
	}
	winner, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(s.name), WithDecryption: aws.Bool(true)})
	if err != nil {
		return cloudSecretMetadata{}, nil, fmt.Errorf("adopt existing AWS SSM parameter: %w", err)
	}
	if winner == nil || winner.Parameter == nil {
		return cloudSecretMetadata{}, nil, errors.New("adopt existing AWS SSM parameter: backend returned no parameter")
	}
	if winner.Parameter.Type != ssmtypes.ParameterTypeSecureString || winner.Parameter.Value == nil || aws.ToString(winner.Parameter.Value) == "" {
		return cloudSecretMetadata{}, nil, fmt.Errorf("adopt existing AWS SSM parameter: parameter must be a non-empty SecureString")
	}
	return cloudSecretMetadata{s.identity, strconv.FormatInt(winner.Parameter.Version, 10)}, []byte(aws.ToString(winner.Parameter.Value)), nil
}

// Metadata returns the current parameter version without decrypting its value.
func (s *awsSSMStore) Metadata(ctx context.Context) (cloudSecretMetadata, error) {
	out, err := s.client.DescribeParameters(ctx, &ssm.DescribeParametersInput{ParameterFilters: []ssmtypes.ParameterStringFilter{{Key: aws.String("Name"), Option: aws.String("Equals"), Values: []string{s.name}}}})
	if err != nil {
		return cloudSecretMetadata{}, fmt.Errorf("read AWS SSM parameter metadata: %w", err)
	}
	if out == nil {
		return cloudSecretMetadata{}, errors.New("read AWS SSM parameter metadata: backend returned no parameter")
	}
	if len(out.Parameters) != 1 || aws.ToString(out.Parameters[0].Name) != s.name {
		return cloudSecretMetadata{}, fmt.Errorf("AWS SSM parameter is absent")
	}
	if out.Parameters[0].Type != ssmtypes.ParameterTypeSecureString {
		return cloudSecretMetadata{}, fmt.Errorf("AWS SSM parameter is not a SecureString")
	}
	return cloudSecretMetadata{s.identity, strconv.FormatInt(out.Parameters[0].Version, 10)}, nil
}

// gcpSecretStore stores opaque values in Google Secret Manager.
type gcpSecretStore struct {
	client           gcpSecretManagerAPI
	parent, secretID string
	name, identity   string
	labels           map[string]string
}

// Close closes the Google Secret Manager client.
func (s *gcpSecretStore) Close() error { return s.client.Close() }

// gcpSecretManagerAPI is the Google Secret Manager API used by the resource.
type gcpSecretManagerAPI interface {
	CreateSecret(context.Context, *secretmanagerpb.CreateSecretRequest, ...gax.CallOption) (*secretmanagerpb.Secret, error)
	AddSecretVersion(context.Context, *secretmanagerpb.AddSecretVersionRequest, ...gax.CallOption) (*secretmanagerpb.SecretVersion, error)
	AccessSecretVersion(context.Context, *secretmanagerpb.AccessSecretVersionRequest, ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error)
	GetSecretVersion(context.Context, *secretmanagerpb.GetSecretVersionRequest, ...gax.CallOption) (*secretmanagerpb.SecretVersion, error)
	Close() error
}

// CreateOrAdopt creates the Google secret and first version or adopts the latest version.
func (s *gcpSecretStore) CreateOrAdopt(ctx context.Context, value []byte) (cloudSecretMetadata, []byte, error) {
	created, err := s.client.CreateSecret(ctx, &secretmanagerpb.CreateSecretRequest{Parent: s.parent, SecretId: s.secretID, Secret: &secretmanagerpb.Secret{Labels: s.labels, Replication: &secretmanagerpb.Replication{Replication: &secretmanagerpb.Replication_Automatic_{Automatic: &secretmanagerpb.Replication_Automatic{}}}}})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		return cloudSecretMetadata{}, nil, fmt.Errorf("create GCP secret: %w", err)
	}
	if err != nil {
		winner, accessErr := s.client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{Name: s.name + "/versions/latest"})
		if accessErr == nil {
			return gcpSecretWinner(s.identity, winner)
		}
		if status.Code(accessErr) == codes.NotFound {
			return cloudSecretMetadata{}, nil, errors.New("adopt existing GCP secret: no version exists; confirm no provisioning process is active, then delete the empty secret and retry")
		}
		return cloudSecretMetadata{}, nil, fmt.Errorf("adopt existing GCP secret current version: %w", accessErr)
	}
	if created == nil || created.Name == "" {
		return cloudSecretMetadata{}, nil, errors.New("create GCP secret: backend returned no secret")
	}
	version, err := s.client.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{Parent: s.name, Payload: &secretmanagerpb.SecretPayload{Data: value}})
	if err != nil {
		winner, accessErr := s.client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{Name: s.name + "/versions/latest"})
		if accessErr == nil {
			return gcpSecretWinner(s.identity, winner)
		}
		return cloudSecretMetadata{}, nil, fmt.Errorf("add initial GCP secret version: %w; the result is indeterminate and the empty secret must not be reused automatically", err)
	}
	if version == nil || version.Name == "" {
		return cloudSecretMetadata{}, nil, errors.New("add initial GCP secret version: backend returned no version")
	}
	return cloudSecretMetadata{s.identity, version.Name}, value, nil
}

// gcpSecretWinner validates and returns an existing Google Secret Manager version.
func gcpSecretWinner(identity string, winner *secretmanagerpb.AccessSecretVersionResponse) (cloudSecretMetadata, []byte, error) {
	if winner == nil || winner.Name == "" || winner.Payload == nil || len(winner.Payload.Data) == 0 {
		return cloudSecretMetadata{}, nil, errors.New("adopt existing GCP secret current version: backend returned an empty value or version name")
	}
	return cloudSecretMetadata{identity, winner.Name}, winner.Payload.Data, nil
}

// Metadata returns the latest enabled Google secret version without reading its value.
func (s *gcpSecretStore) Metadata(ctx context.Context) (cloudSecretMetadata, error) {
	version, err := s.client.GetSecretVersion(ctx, &secretmanagerpb.GetSecretVersionRequest{Name: s.name + "/versions/latest"})
	if err != nil {
		return cloudSecretMetadata{}, fmt.Errorf("read GCP secret version metadata: %w", err)
	}
	if version == nil || version.Name == "" {
		return cloudSecretMetadata{}, errors.New("read GCP secret version metadata: backend returned no version")
	}
	if version.State != secretmanagerpb.SecretVersion_ENABLED {
		return cloudSecretMetadata{}, fmt.Errorf("GCP secret current version is not enabled")
	}
	return cloudSecretMetadata{s.identity, version.Name}, nil
}
