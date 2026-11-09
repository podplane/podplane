// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfstate

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/oidcconfig"
)

var (
	// errBucketAbsent reports that the configured remote-state bucket does not exist.
	errBucketAbsent = errors.New("does not exist")
	// errBucketIncomplete reports that Podplane created the remote-state
	// bucket but an earlier run stopped before configuring it.
	errBucketIncomplete = errors.New("was not fully configured by an earlier run")
)

// Backend identifies the object store and configuration holding the state of
// one generated stack.
type Backend struct {
	// Name identifies the stack, such as "cluster" or "oidc". It names the
	// state object and the generated backend configuration file.
	Name    string
	Kind    string
	Bucket  string
	Region  string
	Account string
	Profile string
	Project string
	// hostname is the normalized OIDC deployment identity; cluster buckets
	// retain their existing naming and ownership policy.
	hostname string
}

// ForCluster derives the remote-state backend for a validated cluster
// configuration.
func ForCluster(cfg *clusterconfig.ClusterConfig) (Backend, error) {
	if len(cfg.Cluster.Providers) != 1 {
		return Backend{}, fmt.Errorf("remote state requires exactly one cloud provider")
	}
	p := cfg.Cluster.Providers[0]
	return newBackend("cluster", cfg.Cluster.ID, p.Kind, p.Account, p.Project, p.Region, p.Profile)
}

// ForOIDC derives the remote-state backend for a validated OIDC server
// configuration. Its bucket includes a readable hostname slug and a hash of
// the original hostname, so dots and hyphens cannot alias another server.
func ForOIDC(cfg *oidcconfig.Config) (Backend, error) {
	issuer, err := url.Parse(cfg.IssuerURL())
	if err != nil || issuer.Hostname() == "" {
		return Backend{}, fmt.Errorf("remote state requires a valid OIDC hostname")
	}
	p := cfg.OIDC.Provider
	hostname := strings.TrimSuffix(strings.ToLower(issuer.Hostname()), ".")
	if err := clusterconfig.ValidateDomainName(hostname); err != nil {
		return Backend{}, fmt.Errorf("OIDC state hostname: %w", err)
	}
	b, err := newBackend("oidc", "oidc", p.Kind, p.Account, p.Project, p.Region, p.Profile)
	if err != nil {
		return Backend{}, err
	}
	suffix := strings.TrimPrefix(b.Bucket, "oidc")
	hash := sha256.Sum256([]byte(hostname))
	slug := strings.ReplaceAll(hostname, ".", "-")
	limit := 63 - len(suffix) - 17 // separator plus 16 hex hash characters
	if limit < 1 {
		return Backend{}, fmt.Errorf("account/project identifier is too long for an OIDC state bucket")
	}
	if len(slug) > limit {
		slug = strings.TrimRight(slug[:limit], "-")
	}
	b.Bucket = fmt.Sprintf("%s%s-%x", slug, suffix, hash[:8])
	b.hostname = hostname
	return b, nil
}

// identityMarkers returns the OIDC ownership tags/labels written at bucket
// creation. GCS labels use a full SHA-256 in lowercase base32 to fit 63 bytes.
func (b Backend) identityMarkers() map[string]string {
	if b.hostname == "" {
		return nil
	}
	if b.Kind == "gcs" {
		hash := sha256.Sum256([]byte(b.hostname))
		return map[string]string{
			"podplane-stack":    "oidc",
			"podplane-hostname": strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(hash[:])),
		}
	}
	return map[string]string{"podplane:stack": "oidc", "podplane:hostname": b.hostname}
}

// verifyIdentity rejects mismatched and unmarked OIDC buckets without changing
// them. Explicit adoption requires the operator to verify the state and set
// the expected markers themselves; auto-approval never bypasses this check.
func (b Backend) verifyIdentity(markers map[string]string) error {
	expected := b.identityMarkers()
	missing := false
	for key, want := range expected {
		got := markers[key]
		if got == "" {
			missing = true
		} else if got != want {
			return fmt.Errorf("remote-state bucket %s ownership mismatch: %s=%q, expected %q for %s; refusing to use it", b.Bucket, key, got, want, b.hostname)
		}
	}
	if missing {
		return fmt.Errorf("remote-state bucket %s is missing OIDC ownership markers; to explicitly adopt it, first verify its state belongs to %s (or it is empty), then set these bucket tags/labels: %v", b.Bucket, b.hostname, expected)
	}
	return nil
}

// newBackend returns the backend for the named stack on the given cloud,
// storing its state in the bucket <prefix>-<account or project>-tfstate.
func newBackend(name, prefix, cloud, account, project, region, profile string) (Backend, error) {
	var b Backend
	switch cloud {
	case "aws":
		if account == "" || region == "" {
			return Backend{}, fmt.Errorf("remote state requires AWS account and region")
		}
		b = Backend{Kind: "s3", Bucket: prefix + "-" + account + "-tfstate", Account: account, Profile: profile}
	case "google":
		if project == "" || region == "" {
			return Backend{}, fmt.Errorf("remote state requires Google project and region")
		}
		b = Backend{Kind: "gcs", Bucket: prefix + "-" + project + "-tfstate", Project: project}
	default:
		return Backend{}, fmt.Errorf("remote state does not support provider %q", cloud)
	}
	if len(b.Bucket) > 63 {
		return Backend{}, fmt.Errorf("remote-state bucket name %s is longer than the 63 characters bucket names allow", b.Bucket)
	}
	b.Name, b.Region = name, region
	return b, nil
}

// ConfigFile returns the name of the backend configuration file WriteConfig
// generates beside the stack's managed .tf files.
func (b Backend) ConfigFile() string {
	return "podplane." + b.Name + ".tfbackend"
}

// LockKey returns the engine's lock object for this stack's default workspace.
func (b Backend) LockKey() string {
	if b.Kind == "gcs" {
		return "podplane/" + b.Name + "/default.tflock"
	}
	return "podplane/" + b.Name + ".tfstate.tflock"
}

// WriteConfig writes the non-secret backend settings consumed by tofu init to
// the file named by ConfigFile in dir.
func (b Backend) WriteConfig(dir string) error {
	var content string
	switch b.Kind {
	case "s3":
		content = fmt.Sprintf("bucket = %q\nkey = %q\nregion = %q\nencrypt = true\nuse_lockfile = true\n", b.Bucket, "podplane/"+b.Name+".tfstate", b.Region)
		if b.Profile != "" {
			content += fmt.Sprintf("profile = %q\n", b.Profile)
		}
	case "gcs":
		content = fmt.Sprintf("bucket = %q\nprefix = %q\n", b.Bucket, "podplane/"+b.Name)
	default:
		return fmt.Errorf("unsupported remote-state backend %q", b.Kind)
	}
	if err := os.WriteFile(filepath.Join(dir, b.ConfigFile()), []byte(content), 0o644); err != nil {
		return fmt.Errorf("write OpenTofu backend configuration: %w", err)
	}
	return nil
}

// Check verifies that the backend bucket exists and meets Podplane's
// state-bucket security and OIDC ownership requirements, writing concerns to warn.
// It never creates or changes a bucket.
func (b Backend) Check(ctx context.Context, warn io.Writer) error {
	switch b.Kind {
	case "s3":
		return b.checkS3(ctx, warn)
	case "gcs":
		return googleAccessError(b.checkGCS(ctx, warn))
	default:
		return fmt.Errorf("unsupported remote-state backend %q", b.Kind)
	}
}

// Preflight checks cloud access before preparing a deployment. It never creates
// or repairs a bucket; absent and unfinished buckets are handled by Ensure.
func (b Backend) Preflight(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := b.Check(ctx, io.Discard)
	if errors.Is(err, errBucketAbsent) || errors.Is(err, errBucketIncomplete) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cloud access check failed before Terraform generation: %w", err)
	}
	return nil
}

// Ensure makes sure the backend bucket exists and meets Podplane's
// state-bucket security requirements, writing progress and non-blocking
// concerns to out. When the bucket is absent, it asks confirm a question
// about creating it and creates it only when confirmed. A bucket Podplane
// created but did not finish configuring is configured without asking again.
func (b Backend) Ensure(ctx context.Context, out io.Writer, confirm func(question string) (bool, error)) error {
	err := b.Check(ctx, out)
	if errors.Is(err, errBucketIncomplete) {
		_, _ = fmt.Fprintf(out, "Finishing configuration of remote-state bucket %s, which an earlier run created\n", b.Bucket)
		return b.configureS3(ctx)
	}
	if !errors.Is(err, errBucketAbsent) {
		return err
	}
	bucketName := lipgloss.NewStyle().Bold(true).Render("Bucket Name: " + b.Bucket)
	ok, err := confirm(fmt.Sprintf("Create remote OpenTofu/Terraform state bucket?\n%s\n\nIt will be private, encrypted, and versioned in the configured cloud account/project.", bucketName))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("remote-state bucket creation cancelled")
	}
	switch b.Kind {
	case "s3":
		err = b.createS3(ctx)
	case "gcs":
		err = b.createGCS(ctx)
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Created remote OpenTofu/Terraform state bucket %s\n", b.Bucket)
	return nil
}
