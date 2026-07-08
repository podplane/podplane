// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/clusterdelete"
)

// TFOutput represents one OpenTofu/Terraform JSON output entry.
type TFOutput struct {
	Sensitive bool            `json:"sensitive"`
	Type      json.RawMessage `json:"type"`
	Value     json.RawMessage `json:"value"`
}

// ShardOutput contains Terraform output metadata for one Nstance shard.
type ShardOutput struct {
	ConfigKey string   `json:"config_key"`
	ServerIPs []string `json:"server_ips"`
}

// Options contains AWS cleanup settings parsed from config and Terraform
// outputs.
type Options struct {
	ClusterID    string
	Bucket       string
	Region       string
	Profile      string
	ShardConfigs map[string]ShardOutput
	WaitTimeout  time.Duration
	PollInterval time.Duration
}

// Provider performs AWS-specific cluster delete cleanup.
type Provider struct {
	s3      *s3.Client
	ec2     *ec2.Client
	options Options
}

// New creates an AWS delete provider using the region and profile from the
// cluster provider config.
func New(ctx context.Context, provider clusterconfig.Provider) (*Provider, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(provider.Region),
	}
	if provider.Profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(provider.Profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return &Provider{
		s3:  s3.NewFromConfig(cfg),
		ec2: ec2.NewFromConfig(cfg),
	}, nil
}

// Prepare parses Terraform outputs and stores the AWS cleanup options needed
// by subsequent provider operations.
func (p *Provider) Prepare(ctx context.Context, cfg *clusterconfig.ClusterConfig, outputs []byte) error {
	options, err := ParseOptions(cfg, outputs)
	if err != nil {
		return err
	}
	p.options = *options
	return nil
}

// ParseOptions extracts AWS cleanup settings from cluster config and
// OpenTofu/Terraform output JSON.
func ParseOptions(cfg *clusterconfig.ClusterConfig, outputs []byte) (*Options, error) {
	var raw map[string]TFOutput
	if err := json.Unmarshal(outputs, &raw); err != nil {
		return nil, fmt.Errorf("parse OpenTofu/Terraform outputs: %w", err)
	}
	var bucket string
	if out, ok := raw["nstance_bucket"]; ok {
		if err := json.Unmarshal(out.Value, &bucket); err != nil {
			return nil, fmt.Errorf("parse nstance_bucket output: %w", err)
		}
	}
	if bucket == "" {
		return nil, fmt.Errorf("missing nstance_bucket output; rerun podplane cluster create --no-apply to regenerate managed .tf files and apply them")
	}
	shards := map[string]ShardOutput{}
	if out, ok := raw["nstance_shards"]; ok {
		if err := json.Unmarshal(out.Value, &shards); err != nil {
			return nil, fmt.Errorf("parse nstance_shards output: %w", err)
		}
	}
	if len(shards) == 0 {
		return nil, fmt.Errorf("missing nstance_shards output; rerun podplane cluster create --no-apply to regenerate managed .tf files and apply them")
	}
	provider := cfg.Cluster.Providers[0]
	return &Options{
		ClusterID:    cfg.Cluster.ID,
		Bucket:       bucket,
		Region:       provider.Region,
		Profile:      provider.Profile,
		ShardConfigs: shards,
		WaitTimeout:  20 * time.Minute,
		PollInterval: 15 * time.Second,
	}, nil
}

// ClusterID returns the configured cluster ID for status messages and AWS tag
// filters.
func (p *Provider) ClusterID() string {
	return p.options.ClusterID
}

// ScaleManagedGroupsToZero updates each shard config in S3 so managed groups
// scale down before infrastructure destroy.
func (p *Provider) ScaleManagedGroupsToZero(ctx context.Context) error {
	for _, shard := range sortedShardNames(p.options.ShardConfigs) {
		configKey := p.options.ShardConfigs[shard].ConfigKey
		if configKey == "" {
			return fmt.Errorf("shard %s has empty config_key output", shard)
		}
		if err := p.scaleShardConfigToZero(ctx, p.options.Bucket, configKey); err != nil {
			return fmt.Errorf("scale shard %s groups to zero: %w", shard, err)
		}
	}
	return nil
}

// scaleShardConfigToZero updates one shard config object in S3, preserving the
// object when no group sizes need changing.
func (p *Provider) scaleShardConfigToZero(ctx context.Context, bucket, key string) error {
	got, err := p.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		return fmt.Errorf("get s3://%s/%s: %w", bucket, key, err)
	}
	defer func() { _ = got.Body.Close() }()
	body, err := io.ReadAll(got.Body)
	if err != nil {
		return fmt.Errorf("read s3://%s/%s: %w", bucket, key, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("parse s3://%s/%s: %w", bucket, key, err)
	}
	changed := zeroGroups(doc)
	if !changed {
		return nil
	}
	next, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal updated shard config: %w", err)
	}
	input := &s3.PutObjectInput{
		Bucket:      awssdk.String(bucket),
		Key:         awssdk.String(key),
		Body:        bytes.NewReader(next),
		ContentType: awssdk.String("application/json"),
	}
	if got.ETag != nil {
		input.IfMatch = got.ETag
	}
	if _, err := p.s3.PutObject(ctx, input); err != nil {
		return fmt.Errorf("put s3://%s/%s: %w", bucket, key, err)
	}
	return nil
}

// zeroGroups mutates a shard config document so every group size is zero and
// reports whether anything changed.
func zeroGroups(doc map[string]any) bool {
	groups, ok := doc["groups"].(map[string]any)
	if !ok {
		return false
	}
	changed := false
	for _, tenantValue := range groups {
		tenant, ok := tenantValue.(map[string]any)
		if !ok {
			continue
		}
		for _, groupValue := range tenant {
			group, ok := groupValue.(map[string]any)
			if !ok {
				continue
			}
			if group["size"] != float64(0) {
				group["size"] = float64(0)
				changed = true
			}
		}
	}
	return changed
}

// WaitForManagedInstancesGone polls AWS until no Nstance-managed EC2
// instances remain for the cluster.
func (p *Provider) WaitForManagedInstancesGone(ctx context.Context) error {
	timeout := p.options.WaitTimeout
	if timeout == 0 {
		timeout = 20 * time.Minute
	}
	interval := p.options.PollInterval
	if interval == 0 {
		interval = 15 * time.Second
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		instances, err := p.ListTaggedInstances(deadlineCtx, true)
		if err != nil {
			return err
		}
		if len(instances) == 0 {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-deadlineCtx.Done():
			timer.Stop()
			return fmt.Errorf("timed out waiting for %d Nstance-managed EC2 instance(s) to terminate: %s", len(instances), clusterdelete.FormatInstances(instances))
		case <-timer.C:
		}
	}
}

// RemainingManagedInstances lists Nstance-managed EC2 instances still present
// after scale-down.
func (p *Provider) RemainingManagedInstances(ctx context.Context) ([]clusterdelete.Instance, error) {
	return p.ListTaggedInstances(ctx, true)
}

// ListTaggedInstances lists EC2 instances tagged for the cluster, optionally
// limited to Nstance-managed instances.
func (p *Provider) ListTaggedInstances(ctx context.Context, managedOnly bool) ([]clusterdelete.Instance, error) {
	filters := []ec2types.Filter{
		{Name: awssdk.String("tag:nstance:cluster-id"), Values: []string{p.options.ClusterID}},
		{Name: awssdk.String("instance-state-name"), Values: []string{"pending", "running", "stopping", "stopped", "shutting-down"}},
	}
	if managedOnly {
		filters = append(filters, ec2types.Filter{Name: awssdk.String("tag:nstance:managed"), Values: []string{"true"}})
	}
	var out []clusterdelete.Instance
	paginator := ec2.NewDescribeInstancesPaginator(p.ec2, &ec2.DescribeInstancesInput{Filters: filters})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list EC2 instances tagged nstance:cluster-id=%s: %w", p.options.ClusterID, err)
		}
		for _, reservation := range page.Reservations {
			for _, inst := range reservation.Instances {
				out = append(out, convertInstance(inst))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// TerminateInstances directly terminates the supplied EC2 instances.
func (p *Provider) TerminateInstances(ctx context.Context, instances []clusterdelete.Instance) error {
	if len(instances) == 0 {
		return nil
	}
	ids := make([]string, 0, len(instances))
	for _, inst := range instances {
		ids = append(ids, inst.ID)
	}
	if _, err := p.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: ids}); err != nil {
		return fmt.Errorf("terminate dangling EC2 instances: %w", err)
	}
	return nil
}

// convertInstance maps an AWS EC2 instance into the provider-neutral instance
// shape used by clusterdelete.
func convertInstance(inst ec2types.Instance) clusterdelete.Instance {
	item := clusterdelete.Instance{
		ID:    awssdk.ToString(inst.InstanceId),
		State: string(inst.State.Name),
	}
	for _, tag := range inst.Tags {
		key := awssdk.ToString(tag.Key)
		value := awssdk.ToString(tag.Value)
		switch key {
		case "Name":
			item.Name = value
		case "nstance:shard":
			item.Shard = value
		case "nstance:group":
			item.Group = value
		case "nstance:managed":
			item.Reason = "nstance-managed"
		}
	}
	return item
}

// sortedShardNames returns shard names in stable order for deterministic S3
// updates and tests.
func sortedShardNames(shards map[string]ShardOutput) []string {
	names := make([]string, 0, len(shards))
	for name := range shards {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
