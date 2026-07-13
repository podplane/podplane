// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/execwrap"
	"github.com/podplane/podplane/internal/health"
	"github.com/podplane/podplane/internal/kubectl"
	"github.com/podplane/podplane/internal/local"
	"github.com/podplane/podplane/internal/oidcserver"
	"github.com/podplane/podplane/internal/tui"
	"github.com/podplane/podplane/internal/vm"
	"github.com/podplane/podplane/pkg/seeds"
	"github.com/spf13/cobra"
)

var localStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a local cluster VM",
	Long:  `Create and start a local cluster VM`,
}

func init() {
	localStartCmd.Flags().StringVarP(&localStartCPUs, "cpus", "c", "2", "CPU cores to allocate to the VM (default 2)")
	localStartCmd.Flags().StringVarP(&localStartMemory, "memory", "m", "4G", "Memory to allocate to the VM (default 4G)")
	localStartCmd.Flags().BoolVar(&localStartConsole, "console", false, "Attach to the VM serial console after startup")
	localStartCmd.Flags().BoolVar(&localStartFollow, "follow", false, "Stream cloud-init user-data logs while waiting for startup")
	localStartCmd.Flags().StringVar(&localStartComponents, "components", seeds.Recommended, "Initial platform components seeded on first boot: recommended, minimal, or none")
}

var (
	localStartCPUs       string
	localStartMemory     string
	localStartConsole    bool
	localStartFollow     bool
	localStartComponents string
)

// newLocalStartCmd creates the `local start` command. After the VM is up it
// configures kubectl (cluster + credentials + context) so that the very next
// `kubectl` command invokes the `podplane hooks kubectl-auth` plugin,
// which performs the OIDC login.
func newLocalStartCmd(c *config.Config) *cobra.Command {
	localStartCmd.RunE = func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		// Create local cluster manager and start the VM
		manager := local.NewManager(c, localClusterID)
		running, err := manager.Running()
		if err != nil {
			return fmt.Errorf("check local VM status: %w", err)
		}
		vmExists := running
		if !vmExists {
			vmExists, err = manager.Exists()
			if err != nil {
				return fmt.Errorf("check local VM exists: %w", err)
			}
		}
		if vmExists && cmd.Flags().Changed("components") {
			return fmt.Errorf("--components can only be used when creating a new local cluster; existing local cluster %q already has its initial components recorded in cluster.jsonc", localClusterID)
		}
		if running {
			_, _ = fmt.Fprintln(os.Stdout, "VM is already running")
			if localStartConsole {
				if err := manager.Console(); err != nil {
					return fmt.Errorf("attach console: %w", err)
				}
			}
			return nil
		}
		if err := local.CheckServerRuntimeDependencies(); err != nil {
			return fmt.Errorf("local server dependency check failed: %w", err)
		}
		startOpts := local.StartOptions{
			CPUs:               localStartCPUs,
			Memory:             localStartMemory,
			StreamUserdataLogs: localStartFollow,
			Components:         localStartComponents,
			RunDownloadProgress: func(run func(progress func(deps.DownloadEvent)) error) error {
				return tui.RunDownloadProgress("Downloading Podplane dependencies", run)
			},
		}
		var stashPath string
		var cluster *clusterconfig.ClusterConfig
		hostSetupDone := false
		configureGitServerAuth := !vmExists
		if !localStartFollow {
			kubeContext := kubectl.ContextKey(localClusterID, true)
			var seedName string
			seedName, err = localStartSeedName(manager, vmExists, localStartComponents)
			if err != nil {
				return err
			}
			checks := health.LocalStartChecks(health.LocalStartOptions{SeedName: seedName, KubeContext: kubeContext, LocalIngressURL: manager.LocalIngressURL})
			items := []tui.TaskProgressItem{
				{Key: "server", Name: "local server", Expected: 2 * time.Second, Timeout: 10 * time.Second},
				{Key: "vm-image", Name: "VM image", Group: "VM", Exclude: vmExists, Success: "created", Expected: time.Second, Timeout: 30 * time.Second},
				{Key: "vm", Name: "VM boot", Group: "VM", Success: "started", Expected: 2 * time.Second, Timeout: 30 * time.Second},
				{Key: "cloud-init", Name: "cloud-init user-data", Group: "VM", Exclude: vmExists, Success: "completed", Expected: 12 * time.Second, Timeout: 10 * time.Minute},
				{Key: "system-services", Name: "systemd services", Group: "VM", Success: "started", Expected: 2 * time.Second, Timeout: 2 * time.Minute},
				{Key: "https-forward", Name: "local HTTPS server forwarder", Group: "VM", Success: "configured", Expected: time.Second, Timeout: 30 * time.Second},
				{Key: "nstance-agent", Name: "nstance", Group: "VM", Success: "registered", Expected: 5 * time.Second, Timeout: 2 * time.Minute},
				{Key: "netsy", Name: "netsy", Group: "VM", Success: "healthy", Expected: 5 * time.Second, Timeout: 2 * time.Minute},
				{Key: "api-live", Name: "kubernetes live", Group: "VM", Success: "live", Expected: 4 * time.Second, Timeout: 2 * time.Minute},
				{Key: "api-ready", Name: "kubernetes ready", Group: "VM", Success: "ready", Expected: 2 * time.Second, Timeout: 2 * time.Minute},
				{Key: "kubectl", Name: fmt.Sprintf("kubectl works with context %q", kubeContext), Ready: true, Success: "ready"},
				{Key: "kubectl-test", Name: "kubectl test", Success: "ready", Expected: 2 * time.Second, Timeout: 30 * time.Second},
				{Key: "local-git-flux-auth", Name: "local git server auth", Exclude: seedName != seeds.Recommended || !configureGitServerAuth, Success: "configured", Expected: time.Second, Timeout: 30 * time.Second},
				{Key: "deploy-ready", Name: "podplane deploy can publish apps to this cluster", Ready: true, Success: "ready"},
				{Key: "ingress-ready", Name: fmt.Sprintf("deployed app hostnames will resolve under *.%s.localhost", localClusterID), Ready: true, Success: "ready"},
			}
			items = append(items, localStartCheckProgressItems(checks)...)
			err = tui.RunTaskProgress(tui.TaskProgressOptions{
				Title:     "Podplane Local",
				Subtitle:  fmt.Sprintf("Starting your local Podplane cluster %q", localClusterID),
				DoneTitle: fmt.Sprintf("✓ Local Podplane cluster %q is ready", localClusterID),
			}, items, func(progress tui.TaskProgress) error {
				startOpts.Progress = progress
				var startErr error
				stashPath, startErr = manager.Start(startOpts)
				if startErr != nil {
					return startErr
				}
				if stashPath == "" {
					return nil
				}
				var configureErr error
				cluster, configureErr = configureLocalKubectl(stashPath, manager, progress)
				if configureErr != nil {
					return configureErr
				}
				if testErr := testLocalKubectl(cluster, progress); testErr != nil {
					return testErr
				}
				if configureGitServerAuth {
					if authErr := configureLocalGitServerFluxAuth(cluster, manager, progress); authErr != nil {
						return authErr
					}
				}
				hostSetupDone = true
				if len(checks) > 0 {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
					defer cancel()
					if _, healthErr := tui.RunHealthTaskProgress(ctx, checks, progress); healthErr != nil {
						return healthErr
					}
				}
				progress.Done("deploy-ready", "podplane deploy can publish apps to this cluster", "ready")
				progress.Done("ingress-ready", fmt.Sprintf("deployed app hostnames will resolve under *.%s.localhost", cluster.Cluster.ID), "ready")
				return nil
			})
		} else {
			stashPath, err = manager.Start(startOpts)
		}
		if err != nil {
			if errors.Is(err, vm.ErrAlreadyRunning) {
				_, _ = fmt.Fprintln(os.Stdout, "VM is already running")
				return nil
			}
			return fmt.Errorf("failed to start: %w", err)
		}
		if stashPath == "" {
			return nil
		}
		if cluster == nil {
			cluster, err = configureLocalKubectl(stashPath, manager, nil)
			if err != nil {
				return err
			}
		}
		if !hostSetupDone {
			if err := testLocalKubectl(cluster, nil); err != nil {
				return err
			}
			if configureGitServerAuth {
				if err := configureLocalGitServerFluxAuth(cluster, manager, nil); err != nil {
					return err
				}
			}
		}
		kubeContext := kubectl.ContextKey(cluster.Cluster.ID, true)
		if localStartFollow {
			fmt.Printf("✓ kubectl configured for local cluster using %q context\n", kubeContext)
			fmt.Println("You can now use kubectl with this cluster.")
			checks := health.LocalStartChecks(health.LocalStartOptions{SeedName: cluster.Cluster.Seed.Name, KubeContext: kubeContext, LocalIngressURL: manager.LocalIngressURL})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if err := tui.RunHealthProgress(tui.HealthProgressOptions{
				Context:        ctx,
				Title:          "Checking local cluster components",
				ShowTiming:     true,
				SuccessMessage: "Local cluster components ready",
			}, checks); err != nil {
				return fmt.Errorf("local cluster health check failed: %w", err)
			}
		}
		printLocalStartDone(cluster.Cluster.ID)
		if cluster.Cluster.Seed.Name == seeds.Recommended {
			if _, err := manager.LocalIngressURL(); err != nil {
				return fmt.Errorf("get local ingress URL: %w", err)
			}
			if trusted, err := local.MkcertTrustInstalled(); err == nil && !trusted {
				fmt.Println("For browsers to trust local ingress HTTPS certificates, run `mkcert -install` once.")
			}
		}
		if localStartConsole {
			if err := manager.Console(); err != nil {
				return fmt.Errorf("attach console: %w", err)
			}
		}
		return nil
	}

	return localStartCmd
}

// localStartSeedName returns the seed name that local start will use for this
// run. New clusters use --components; existing clusters use the recorded seed
// because seeding is first-boot-only.
func localStartSeedName(manager *local.Local, vmExists bool, requested string) (string, error) {
	if !vmExists {
		seedName, err := seeds.ParseName(requested)
		if err != nil {
			return "", fmt.Errorf("invalid --components: %w", err)
		}
		return seedName, nil
	}
	seed, err := manager.SeedConfig()
	if err != nil {
		return "", err
	}
	return seed.Name, nil
}

// configureLocalKubectl loads the generated local cluster config, refreshes the
// local OIDC token cache, and configures kubeconfig. When progress is provided
// it reports kubectl readiness into the combined local-start TUI and suppresses
// kubectl's line-oriented stdout; when progress is nil it preserves the normal
// line-oriented output used by --follow and fallback paths.
func configureLocalKubectl(stashPath string, manager *local.Local, progress tui.TaskProgress) (*clusterconfig.ClusterConfig, error) {
	cluster, err := clusterconfig.Load(stashPath)
	if err != nil {
		return nil, fmt.Errorf("load local cluster config: %w", err)
	}
	if progress != nil {
		progress.Started("kubectl", fmt.Sprintf("kubectl works with context %q", kubectl.ContextKey(cluster.Cluster.ID, true)), "")
	}
	// Local OIDC always issues tokens with sub == oidcserver.LocalSub, so we
	// can configure kubectl now without first performing a login. We also
	// refresh the cached token too, as local server ports can change across
	// restarts, making an old-but-unexpired token's issuer stale.
	key, err := oidcserver.LoadOrCreateKeypair(manager.OIDCKeyPath())
	if err != nil {
		return nil, fmt.Errorf("load local OIDC keypair: %w", err)
	}
	idToken, err := oidcserver.IssueLocalToken(key, cluster.Cluster.OIDC.IssuerURL, cluster.Cluster.ID)
	if err != nil {
		return nil, fmt.Errorf("issue local kubectl token: %w", err)
	}
	localAuthConfig, restoreKeyringPass, err := config.InitWithLocalKeyring()
	if err != nil {
		return nil, err
	}
	defer restoreKeyringPass()
	if err := localAuthConfig.AuthSet(config.AuthMetadata{
		Sub:         oidcserver.LocalSub,
		ClusterID:   cluster.Cluster.ID,
		ClusterName: cluster.Cluster.Name,
		Issuer:      cluster.Cluster.OIDC.IssuerURL,
		ClientID:    cluster.ResolvedClientID(),
		UserEmail:   "test@localhost",
	}, config.AuthSecrets{IDToken: idToken}, true); err != nil {
		return nil, fmt.Errorf("cache local kubectl token: %w", err)
	}
	if err := localAuthConfig.SetClusterSummary(config.ClusterSummaryFromConfig(cluster), true); err != nil {
		return nil, fmt.Errorf("cache local cluster summary: %w", err)
	}
	stdout := io.Writer(os.Stdout)
	if progress != nil {
		stdout = io.Discard
	}
	kubeAPICAPath, err := manager.LocalIngressCACertPath()
	if err != nil {
		return nil, fmt.Errorf("prepare local kubernetes api CA cert: %w", err)
	}
	kubernetesAPIURL, err := manager.LocalKubernetesAPIURL()
	if err != nil {
		return nil, fmt.Errorf("derive local kubernetes api URL: %w", err)
	}
	if err := kubectl.ConfigureClusterAccess(stdout, cluster.Cluster.ID, kubernetesAPIURL, oidcserver.LocalSub, kubeAPICAPath, true); err != nil {
		return nil, fmt.Errorf("configure kubectl: %w", err)
	}
	if progress != nil {
		progress.Done("kubectl", fmt.Sprintf("kubectl works with context %q", kubectl.ContextKey(cluster.Cluster.ID, true)), "ready")
	}
	return cluster, nil
}

// testLocalKubectl verifies the configured local kubectl context can reach the
// cluster API from the host.
func testLocalKubectl(cluster *clusterconfig.ClusterConfig, progress tui.TaskProgress) error {
	name := "kubectl test"
	if progress != nil {
		progress.Started("kubectl-test", name, "")
	}
	kubeContext := kubectl.ContextKey(cluster.Cluster.ID, true)
	args := append(kubectl.Args(kubeContext, ""), "get", "--raw=/readyz")
	cmd := execwrap.Command("kubectl", args...)
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		wrapped := &kubectl.Error{Stage: "test", Err: err, Stderr: strings.TrimSpace(stderr.String())}
		if progress != nil {
			progress.Failed("kubectl-test", name, wrapped)
		}
		return wrapped
	}
	if progress != nil {
		progress.Done("kubectl-test", name, "ready")
	}
	return nil
}

// configureLocalGitServerFluxAuth applies the local Flux Git CA Secret for
// recommended seeded clusters that source components from the local Git server.
func configureLocalGitServerFluxAuth(cluster *clusterconfig.ClusterConfig, manager *local.Local, progress tui.TaskProgress) error {
	if cluster.Cluster.Seed.Name != seeds.Recommended {
		return nil
	}
	name := "local git server auth"
	if progress != nil {
		progress.Started("local-git-flux-auth", name, "")
	}
	source := cluster.Cluster.Components.Source
	if source == nil || source.SecretRef == nil || source.SecretRef.Name == "" {
		if progress != nil {
			progress.Skipped("local-git-flux-auth", name, "not needed")
		}
		return nil
	}
	caPEM, err := os.ReadFile(manager.OIDCCACertPath())
	if err != nil {
		wrapped := fmt.Errorf("read local server CA certificate: %w", err)
		if progress != nil {
			progress.Failed("local-git-flux-auth", name, wrapped)
		}
		return wrapped
	}
	manifest, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]string{
			"name":      source.SecretRef.Name,
			"namespace": "platform-components",
		},
		"type": "Opaque",
		"data": map[string]string{
			"ca.crt": base64.StdEncoding.EncodeToString(caPEM),
		},
	})
	if err != nil {
		return err
	}
	kubeContext := kubectl.ContextKey(cluster.Cluster.ID, true)
	args := append(kubectl.Args(kubeContext, ""), "apply", "-f", "-")
	cmd := execwrap.Command("kubectl", args...)
	var stderr bytes.Buffer
	cmd.Stdin = bytes.NewReader(manifest)
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		wrapped := &kubectl.Error{Stage: "apply local git server auth", Err: err, Stderr: strings.TrimSpace(stderr.String())}
		if progress != nil {
			progress.Failed("local-git-flux-auth", name, wrapped)
		}
		return wrapped
	}
	if progress != nil {
		progress.Done("local-git-flux-auth", name, "configured")
	}
	return nil
}

// localStartCheckProgressItems converts component health checks into task
// progress items so local start can render one combined progress plan.
func localStartCheckProgressItems(checks []health.Check) []tui.TaskProgressItem {
	items := make([]tui.TaskProgressItem, 0, len(checks))
	for _, check := range checks {
		items = append(items, tui.TaskProgressItem{
			Key:      check.Key,
			Name:     check.Name,
			Group:    "components",
			Success:  "ready",
			Expected: check.Expected,
			Timeout:  check.Timeout,
		})
	}
	return items
}

// printLocalStartDone renders the final local-start success summary after the
// live TUI has exited.
func printLocalStartDone(clusterID string) {
	kubeContext := kubectl.ContextKey(clusterID, true)
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(tui.ColorPrimary).
		Padding(1, 2).
		Width(76)
	body := strings.Join([]string{
		fmt.Sprintf("✓ Local Podplane cluster %q is ready", clusterID),
		"",
		"Ready",
		fmt.Sprintf("• kubectl works with context %q", kubeContext),
		"• podplane deploy can publish apps to this cluster",
		fmt.Sprintf("• deployed app hostnames will resolve under *.%s.localhost", clusterID),
	}, "\n")
	fmt.Println(card.Render(body))
	fmt.Println()
	fmt.Println("Try this")
	fmt.Println(`  podplane deploy web --name hello \`)
	fmt.Printf(`    --image %s-registry.local/mirror/ghcr.io/podplane/hello:latest \`+"\n", clusterID)
	fmt.Printf("    --hostname hello.%s.localhost\n", clusterID)
	fmt.Println()
	fmt.Println("After deploy")
	fmt.Printf("  https://hello.%s.localhost:4433 will open your app.\n", clusterID)
}
