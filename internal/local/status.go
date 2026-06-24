// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"context"
	"fmt"
	"strconv"

	"github.com/fatih/color"
)

// StatusReport is the machine-readable local cluster status.
type StatusReport struct {
	ClusterID   string                 `json:"cluster_id"`
	Running     bool                   `json:"running"`
	VM          StatusReportVM         `json:"vm"`
	LocalServer StatusReportServer     `json:"local_server"`
	Components  StatusReportComponents `json:"components"`
}

// StatusReportVM describes the local VM status fields.
type StatusReportVM struct {
	Provider         string `json:"provider"`
	NodeIP           string `json:"node_ip"`
	ForwardHTTPSPort int    `json:"forward_https_port"`
}

// StatusReportServer describes the local server status fields.
type StatusReportServer struct {
	Running    bool   `json:"running"`
	HTTPPort   int    `json:"http_port,omitempty"`
	HTTPSPort  int    `json:"https_port,omitempty"`
	CACertFile string `json:"ca_cert_file,omitempty"`
}

// StatusReportComponents describes the effective local components source.
type StatusReportComponents struct {
	Source StatusReportComponentsSource `json:"source"`
}

// StatusReportComponentsSource describes the effective local components Git source.
type StatusReportComponentsSource struct {
	URL string                          `json:"url"`
	Ref StatusReportComponentsSourceRef `json:"ref"`
}

// StatusReportComponentsSourceRef describes the effective local components Git ref.
type StatusReportComponentsSourceRef struct {
	Branch string `json:"branch,omitempty"`
}

// Running reports whether the local cluster VM currently exists and is running.
func (m *Local) Running() (bool, error) {
	exists, err := m.vm.Exists()
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	return m.vm.Running()
}

// Exists reports whether the local cluster VM has already been created.
func (m *Local) Exists() (bool, error) {
	return m.vm.Exists()
}

// Status checks and displays the status of the local cluster VM
func (m *Local) Status() error {
	// Check if VM exists
	exists, err := m.vm.Exists()
	if err != nil {
		return err
	}

	vmRunning := false
	if !exists {
		color.Red("VM Status: Not created")
	} else {
		// Check VM status
		running, err := m.vm.Running()
		if err != nil {
			return err
		}
		vmRunning = running

		// Display status
		if running {
			if vmPID, err := m.vm.PID(); err == nil && vmPID != 0 {
				color.Green(fmt.Sprintf("VM Status: Running (PID %d)", vmPID))
			} else {
				color.Green("VM Status: Running")
			}
		} else {
			color.Yellow("VM Status: Stopped")
		}
	}

	if vmRunning {
		ctx := context.Background()
		if state, err := m.ProbeCloudInit(ctx); err != nil {
			color.Red(fmt.Sprintf("Cloud-init: unavailable (%v)", err))
		} else {
			switch state {
			case "done":
				color.Green("Cloud-init: done")
			case "error":
				color.Red("Cloud-init: error")
			default:
				color.Yellow(fmt.Sprintf("Cloud-init: %s", state))
			}
		}
		switch {
		case m.ProbeAPIServerLive(ctx) != nil:
			color.Yellow("Kubernetes API: not live")
		case m.ProbeAPIServerReady(ctx) != nil:
			color.Yellow("Kubernetes API: live, not ready")
		default:
			color.Green("Kubernetes API: ready")
		}
	}

	// Check local server status
	pidFile, err := ServerPIDFile(m.runtimeDir)
	if err != nil {
		color.Red("Local Server: Unknown (failed to load PID file)")
		return nil
	}

	isRunning, err := pidFile.IsRunning()
	if err != nil || !isRunning {
		color.Red("Local Server: Not running")
		color.Yellow(fmt.Sprintf("Local Server Log: %s", ServerLogPath(m.runtimeDir)))
		return nil
	}

	host := pidFile.GetData("host")
	httpPort := pidFile.GetData("http_port")
	httpsPort := pidFile.GetData("https_port")
	color.Green(fmt.Sprintf("Local Server: Running (PID %d) at HTTP %s:%s and HTTPS %s:%s", pidFile.PID(), host, httpPort, host, httpsPort))
	if logPath := pidFile.GetData("log_file"); logPath != "" {
		color.Green(fmt.Sprintf("Local Server Log: %s", logPath))
	} else {
		color.Green(fmt.Sprintf("Local Server Log: %s", ServerLogPath(m.runtimeDir)))
	}

	return nil
}

// StatusReport returns the machine-readable local cluster status.
func (m *Local) StatusReport() (StatusReport, error) {
	running, err := m.Running()
	if err != nil {
		return StatusReport{}, err
	}
	report := StatusReport{
		ClusterID: m.clusterID,
		Running:   running,
		VM: StatusReportVM{
			Provider:         "qemu",
			NodeIP:           m.vm.NodeIP(),
			ForwardHTTPSPort: localVMForwardPortToLocalServerHTTPS,
		},
		LocalServer: StatusReportServer{CACertFile: m.OIDCCACertPath()},
		Components: StatusReportComponents{
			Source: StatusReportComponentsSource{
				URL: localGitURL(m.vm.NodeIP(), "components.git"),
				Ref: StatusReportComponentsSourceRef{Branch: "local-dev"},
			},
		},
	}
	pidFile, err := ServerPIDFile(m.runtimeDir)
	if err != nil {
		return report, nil
	}
	serverRunning, err := pidFile.IsRunning()
	if err != nil || !serverRunning {
		return report, nil
	}
	report.LocalServer.Running = true
	report.LocalServer.HTTPPort, _ = strconv.Atoi(pidFile.GetData("http_port"))
	report.LocalServer.HTTPSPort, _ = strconv.Atoi(pidFile.GetData("https_port"))
	return report, nil
}
