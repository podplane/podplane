// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/fakevault"
	"github.com/podplane/podplane/internal/local"
	"github.com/spf13/cobra"
)

var localServerCmd = &cobra.Command{
	Hidden: true,
	Use:    "server",
	Short:  "Run a background server for VMs to access required files and services",
	Long:   `Server is used by VMs to download cloud-init files, cached package files, and access services like fake S3 and OIDC servers. Generally you should not be running this command directly.`,
}

func init() {
	localServerCmd.Flags().StringVarP(&serverAddr, "addr", "a", "0.0.0.0", "Address to bind server to. Default is 0.0.0.0")
	localServerCmd.Flags().BoolVarP(&serverBackground, "background", "b", false, "Run the server in the background. This is set when 'podplane local start' invokes this command in the background.")
	localServerCmd.Flags().BoolVarP(&serverStop, "stop", "q", false, "Stop/exit instead of starting the server. This will kill any exist server process.")
}

var serverAddr string
var serverBackground bool
var serverStop bool

func newLocalServerCmd(c *config.Config) *cobra.Command {
	localServerCmd.RunE = func(cmd *cobra.Command, args []string) error {
		// load pid file
		pidFile, err := local.ServerPIDFile(c.RuntimeDirectory())
		if err != nil {
			return fmt.Errorf("failed to load local server PID file: %w", err)
		}

		// check if exit flag is set
		if serverStop {
			// exit early if pid is zero
			if pidFile.PID() == 0 {
				fmt.Println("No server running.")
				return nil
			}
			return local.ServerKill(pidFile)
		}

		// check if the server is already running
		isRunning, err := pidFile.IsRunning()
		if err != nil {
			return fmt.Errorf("failed to check if local server process is running: %w", err)
		}
		if isRunning {
			// Check if dir/host/ports match.
			if dir := pidFile.GetData("dir"); dir != c.DepsCacheDir() {
				return fmt.Errorf("local server process already running but with a different serve directory: %s", dir)
			}
			host := pidFile.GetData("host")
			if host != serverAddr {
				return fmt.Errorf("local server process already running but with a different host: %s", host)
			}
			httpPort := pidFile.GetData("http_port")
			httpsPort := pidFile.GetData("https_port")
			// already running
			fmt.Printf("Local server already running (PID %d) at HTTP %s:%s and HTTPS %s:%s\n", pidFile.PID(), host, httpPort, host, httpsPort)
			return nil
		}

		// check runtime dependencies
		if err := local.CheckServerRuntimeDependencies(); err != nil {
			return fmt.Errorf("local server runtime dependency check failed: %w", err)
		}

		// configure signal handling for shutdown
		shutdownErrsCh := make(chan error)
		go func() {
			// if a signal is received, push it on to the c channel
			c := make(chan os.Signal, 1)
			signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
			defer signal.Stop(c)
			// block until a signal is received, then push it on to the shutdownErrsCh channel
			shutdownErrsCh <- fmt.Errorf("%s", <-c)
		}()

		// Start a local server.
		vaultStore := fakevault.NewFileStore(c, filepath.Join(c.DataDirectory(), "local"))
		server, err := local.NewServer(pidFile, c, serverAddr, 0, vaultStore)
		if err != nil {
			return fmt.Errorf("failed to start local server: %w", err)
		}
		if !serverBackground {
			fmt.Printf("Local server started (PID %d) at HTTP %s:%d and HTTPS %s:%d\n", os.Getpid(), serverAddr, server.HTTPPort(), serverAddr, server.HTTPSPort())
			if trusted, err := local.MkcertTrustInstalled(); err == nil && !trusted {
				fmt.Println("For browsers to trust local ingress HTTPS certificates, run `mkcert -install` once.")
			}
		}

		// block until a shutdown error is received (err or signal)
		if !serverBackground {
			fmt.Println("Press Ctrl+C to stop the local server")
		}
		<-shutdownErrsCh
		if !serverBackground {
			fmt.Printf("\nStopping local server...\n")
		}
		// stop the server, and exit
		if err := server.Stop(2 * time.Second); err != nil {
			return err
		}
		return nil
	}

	return localServerCmd
}
