// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clustercreate

import (
	"context"
	"errors"
	"fmt"

	"cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/podplane/podplane/internal/clusterconfig"
)

// tunnelStore uses the Nstance secrets backend, not the workload secrets backend.
type tunnelStore interface {
	Exists(context.Context) (bool, error)
	Read(context.Context) ([]byte, error)
	Create(context.Context, []byte) error
	Name() string
	Close() error
}

var errTunnelSecretExists = errors.New("credentials secret already exists")

// newTunnelStore opens the cluster's tunnel secret using its infrastructure credentials.
// The caller must close the store when finished.
func newTunnelStore(ctx context.Context, cfg *clusterconfig.ClusterConfig) (tunnelStore, error) {
	p := cfg.Cluster.Providers[0]
	if p.Kind == "google" {
		client, err := secretmanager.NewClient(ctx)
		if err != nil {
			return nil, err
		}
		return &googleTunnelStore{
			client:  client,
			project: p.Project,
			id:      cfg.Cluster.ID + "-" + cfg.Cluster.Tunnel.CredentialSecret,
		}, nil
	}
	options := []func(*config.LoadOptions) error{config.WithRegion(p.Region)}
	if p.Profile != "" {
		options = append(options, config.WithSharedConfigProfile(p.Profile))
	}
	awsConfig, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, err
	}
	return &awsTunnelStore{
		client: ssm.NewFromConfig(awsConfig),
		name:   "/" + cfg.Cluster.ID + "/" + cfg.Cluster.Tunnel.CredentialSecret,
	}, nil
}

// awsTunnelStore stores credentials as an encrypted Parameter Store value.
type awsTunnelStore struct {
	client *ssm.Client
	name   string
}

// Name returns the full Parameter Store path.
func (s *awsTunnelStore) Name() string { return s.name }

// Close is a no-op because the AWS client owns no resources that need closing.
func (*awsTunnelStore) Close() error { return nil }

// Exists checks metadata without decrypting or reading the parameter value.
func (s *awsTunnelStore) Exists(ctx context.Context) (bool, error) {
	pages := ssm.NewDescribeParametersPaginator(s.client, &ssm.DescribeParametersInput{
		ParameterFilters: []types.ParameterStringFilter{{
			Key:    aws.String("Name"),
			Option: aws.String("Equals"),
			Values: []string{s.name},
		}},
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return false, err
		}
		for _, parameter := range page.Parameters {
			if aws.ToString(parameter.Name) == s.name {
				return true, nil
			}
		}
	}
	return false, nil
}

// Read returns decrypted credentials; the caller should clear the returned bytes.
func (s *awsTunnelStore) Read(ctx context.Context) ([]byte, error) {
	out, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(s.name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return nil, err
	}
	return []byte(aws.ToString(out.Parameter.Value)), nil
}

// Create stores credentials without overwriting an existing parameter.
func (s *awsTunnelStore) Create(ctx context.Context, value []byte) error {
	_, err := s.client.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(s.name),
		Type:      types.ParameterTypeSecureString,
		Value:     aws.String(string(value)),
		Overwrite: aws.Bool(false),
	})
	var exists *types.ParameterAlreadyExists
	if errors.As(err, &exists) {
		return errTunnelSecretExists
	}
	return err
}

// googleTunnelStore stores credentials in a Google Secret Manager secret.
type googleTunnelStore struct {
	client      *secretmanager.Client
	project, id string
}

// Name returns the fully qualified Secret Manager resource name.
func (s *googleTunnelStore) Name() string { return "projects/" + s.project + "/secrets/" + s.id }

// Close releases the Secret Manager client's connections.
func (s *googleTunnelStore) Close() error { return s.client.Close() }

// Exists checks secret metadata without accessing its payload.
func (s *googleTunnelStore) Exists(ctx context.Context) (bool, error) {
	_, err := s.client.GetSecret(ctx, &secretmanagerpb.GetSecretRequest{Name: s.Name()})
	if status.Code(err) == codes.NotFound {
		return false, nil
	}
	return err == nil, err
}

// Read returns the latest credentials; the caller should clear the returned bytes.
func (s *googleTunnelStore) Read(ctx context.Context) ([]byte, error) {
	out, err := s.client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{Name: s.Name() + "/versions/latest"})
	if err != nil {
		return nil, err
	}
	return out.Payload.Data, nil
}

// Create adds a secret and its first version, refusing to modify an existing secret.
// A failed version upload leaves the new secret in place for inspection.
func (s *googleTunnelStore) Create(ctx context.Context, value []byte) error {
	_, err := s.client.CreateSecret(ctx, &secretmanagerpb.CreateSecretRequest{
		Parent:   "projects/" + s.project,
		SecretId: s.id,
		Secret: &secretmanagerpb.Secret{
			Replication: &secretmanagerpb.Replication{
				Replication: &secretmanagerpb.Replication_Automatic_{
					Automatic: &secretmanagerpb.Replication_Automatic{},
				},
			},
		},
	})
	if status.Code(err) == codes.AlreadyExists {
		return errTunnelSecretExists
	}
	if err != nil {
		return err
	}
	_, err = s.client.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{
		Parent:  s.Name(),
		Payload: &secretmanagerpb.SecretPayload{Data: value},
	})
	if err != nil {
		return fmt.Errorf("secret created but credentials upload was not confirmed; inspect the secret before retrying: %w", err)
	}
	return nil
}
