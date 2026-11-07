// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"bytes"
	"context"
	"fmt"
	"io"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/clusterdelete"
)

// Provider implements clusterdelete.Provider with S3 and EC2.
type Provider struct {
	s3  *s3.Client
	ec2 *ec2.Client
}

// New creates an AWS delete provider using the region and profile from the
// cluster provider config.
func New(ctx context.Context, provider clusterconfig.Provider) (*Provider, error) {
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(provider.Region)}
	if provider.Profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(provider.Profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return &Provider{s3: s3.NewFromConfig(cfg), ec2: ec2.NewFromConfig(cfg)}, nil
}

// UpdateObject rewrites an S3 object, conditional on its ETag.
func (p *Provider) UpdateObject(ctx context.Context, bucket, key string, update func([]byte) ([]byte, bool, error)) error {
	got, err := p.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: awssdk.String(bucket), Key: awssdk.String(key)})
	if err != nil {
		return fmt.Errorf("get s3://%s/%s: %w", bucket, key, err)
	}
	defer func() { _ = got.Body.Close() }()
	body, err := io.ReadAll(got.Body)
	if err != nil {
		return fmt.Errorf("read s3://%s/%s: %w", bucket, key, err)
	}
	next, changed, err := update(body)
	if err != nil || !changed {
		return err
	}
	input := &s3.PutObjectInput{
		Bucket:      awssdk.String(bucket),
		Key:         awssdk.String(key),
		Body:        bytes.NewReader(next),
		ContentType: awssdk.String("application/json"),
		IfMatch:     got.ETag,
	}
	if _, err := p.s3.PutObject(ctx, input); err != nil {
		return fmt.Errorf("put s3://%s/%s: %w", bucket, key, err)
	}
	return nil
}

// ManagedInstances lists EC2 instances tagged nstance:cluster-id=clusterID
// and nstance:managed=true that have not terminated.
func (p *Provider) ManagedInstances(ctx context.Context, clusterID string) ([]clusterdelete.Instance, error) {
	filters := []ec2types.Filter{
		{Name: awssdk.String("tag:nstance:cluster-id"), Values: []string{clusterID}},
		{Name: awssdk.String("tag:nstance:managed"), Values: []string{"true"}},
		{Name: awssdk.String("instance-state-name"), Values: []string{"pending", "running", "stopping", "stopped", "shutting-down"}},
	}
	var out []clusterdelete.Instance
	paginator := ec2.NewDescribeInstancesPaginator(p.ec2, &ec2.DescribeInstancesInput{Filters: filters})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list Nstance-managed EC2 instances tagged nstance:cluster-id=%s: %w", clusterID, err)
		}
		for _, reservation := range page.Reservations {
			for _, inst := range reservation.Instances {
				out = append(out, instance(inst))
			}
		}
	}
	return out, nil
}

// Terminate terminates EC2 instances.
func (p *Provider) Terminate(ctx context.Context, instances []clusterdelete.Instance) error {
	ids := make([]string, 0, len(instances))
	for _, inst := range instances {
		ids = append(ids, inst.ID)
	}
	if _, err := p.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: ids}); err != nil {
		return fmt.Errorf("terminate EC2 instances: %w", err)
	}
	return nil
}

// instance converts an EC2 instance to the provider-neutral clusterdelete form.
func instance(inst ec2types.Instance) clusterdelete.Instance {
	item := clusterdelete.Instance{ID: awssdk.ToString(inst.InstanceId), State: string(inst.State.Name)}
	if inst.Placement != nil {
		item.Zone = awssdk.ToString(inst.Placement.AvailabilityZone)
	}
	for _, tag := range inst.Tags {
		switch awssdk.ToString(tag.Key) {
		case "Name":
			item.Name = awssdk.ToString(tag.Value)
		case "nstance:shard":
			item.Shard = awssdk.ToString(tag.Value)
		case "nstance:group":
			item.Group = awssdk.ToString(tag.Value)
		}
	}
	return item
}
