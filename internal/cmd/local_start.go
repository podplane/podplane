// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/deps"
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
		if running {
			fmt.Fprintln(os.Stdout, "VM is already running")
			if localStartConsole {
				if err := manager.Console(); err != nil {
					return fmt.Errorf("attach console: %w", err)
				}
			}
			return nil
		}
		vmExists, err := manager.Exists()
		if err != nil {
			return fmt.Errorf("check local VM exists: %w", err)
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
		if !localStartFollow {
			items := []tui.TaskProgressItem{
				{Key: "server", Name: "Local server", Expected: 2 * time.Second, Timeout: 10 * time.Second},
				{Key: "vm-image", Name: "VM image", Exclude: vmExists, Success: "created", Expected: time.Second, Timeout: 30 * time.Second},
				{Key: "vm", Name: "VM", Success: "started", Expected: 2 * time.Second, Timeout: 30 * time.Second},
				{Key: "cloud-init", Name: "cloud-init user-data", Exclude: vmExists, Success: "completed", Expected: 12 * time.Second, Timeout: 10 * time.Minute},
				{Key: "system-services", Name: "systemd services", Success: "started", Expected: 2 * time.Second, Timeout: 2 * time.Minute},
				{Key: "nstance-agent", Name: "nstance", Success: "registered", Expected: 5 * time.Second, Timeout: 2 * time.Minute},
				{Key: "netsy", Name: "netsy", Success: "healthy", Expected: 5 * time.Second, Timeout: 2 * time.Minute},
				{Key: "api-live", Name: "kubernetes live", Success: "live", Expected: 4 * time.Second, Timeout: 2 * time.Minute},
				{Key: "api-ready", Name: "kubernetes ready", Success: "ready", Expected: 2 * time.Second, Timeout: 2 * time.Minute},
			}
			err = tui.RunTaskProgress("Podplane local start", items, func(progress tui.TaskProgress) error {
				startOpts.Progress = progress
				var startErr error
				stashPath, startErr = manager.Start(startOpts)
				return startErr
			})
		} else {
			stashPath, err = manager.Start(startOpts)
		}
		if err != nil {
			if errors.Is(err, vm.ErrAlreadyRunning) {
				fmt.Fprintln(os.Stdout, "VM is already running")
				return nil
			}
			return fmt.Errorf("failed to start: %w", err)
		}
		if stashPath == "" {
			return nil
		}
		cluster, err := clusterconfig.Load(stashPath)
		if err != nil {
			return fmt.Errorf("load local cluster config: %w", err)
		}
		// Local OIDC always issues tokens with sub == oidcserver.LocalSub, so we
		// can configure kubectl now without first performing a login. We also
		// refresh the cached token too, as local server ports can change across
		// restarts, making an old-but-unexpired token's issuer stale.
		key, err := oidcserver.LoadOrCreateKeypair(manager.OIDCKeyPath())
		if err != nil {
			return fmt.Errorf("load local OIDC keypair: %w", err)
		}
		idToken, err := oidcserver.IssueLocalToken(key, cluster.Cluster.OIDC.IssuerURL, cluster.Cluster.ID)
		if err != nil {
			return fmt.Errorf("issue local kubectl token: %w", err)
		}
		localAuthConfig, restoreKeyringPass, err := config.InitWithLocalKeyring()
		if err != nil {
			return err
		}
		defer restoreKeyringPass()
		if err := localAuthConfig.AuthSet(config.AuthMetadata{
			Sub:         oidcserver.LocalSub,
			ClusterID:   cluster.Cluster.ID,
			ClusterName: cluster.Cluster.Name,
			Issuer:      cluster.Cluster.OIDC.IssuerURL,
			ClientID:    cluster.ResolvedClientID(),
			UserEmail:   "test@localhost",
		}, config.AuthSecrets{IDToken: idToken}); err != nil {
			return fmt.Errorf("cache local kubectl token: %w", err)
		}
		if err := kubectl.ConfigureClusterAccess(os.Stdout, cluster.Cluster.ID, cluster.ResolvedKubernetesAPIURL(), oidcserver.LocalSub, "", true); err != nil {
			return fmt.Errorf("configure kubectl: %w", err)
		}
		kubeContext := kubectl.ContextKey(cluster.Cluster.ID, true)
		fmt.Printf("✓ kubectl configured for local cluster using %q context\n", kubeContext)
		fmt.Println("You can now use kubectl with this cluster.")
		checks := health.LocalStartChecks(health.LocalStartOptions{SeedName: cluster.Cluster.Seed.Name, KubeContext: kubeContext, LocalIngressURL: manager.LocalIngressURL})
		if len(checks) > 0 {
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
		if cluster.Cluster.Seed.Name == seeds.Recommended {
			ingressURL, err := manager.LocalIngressURL()
			if err != nil {
				return fmt.Errorf("get local ingress URL: %w", err)
			}
			fmt.Printf("Local ingress proxy: %s\n", ingressURL)
			if trusted, err := local.MkcertTrustInstalled(); err == nil && !trusted {
				fmt.Println("For browsers to trust local ingress HTTPS certificates, run `mkcert -install` once.")
			}
			fmt.Printf("Deploy example:\n\tpodplane deploy web --name hello --image ghcr.io/podplane/hello:latest --hostname hello.%s.localhost\n", cluster.Cluster.ID)
			fmt.Println("✓ Recommended local cluster components are ready; ingress, kubectl, and podplane deploy are ready to use.")
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
