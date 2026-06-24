// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/fatih/color"
	"github.com/puidv7/puidv7-go"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/osboot"
	"github.com/podplane/podplane/internal/tui"
	"github.com/podplane/podplane/internal/userdata"
	"github.com/podplane/podplane/internal/vm"
	"github.com/podplane/podplane/pkg/seeds"
)

// StartOptions controls local cluster startup.
type StartOptions struct {
	CPUs                string
	Memory              string
	StreamUserdataLogs  bool
	Components          string
	RunDownloadProgress func(run func(progress func(deps.DownloadEvent)) error) error
	Progress            tui.TaskProgress
}

// configureLocalHTTPSForwarderScript updates the socat systemd unit rendered
// by user-data.sh when the provider = local and sets the host-side local HTTPS
// server port to the current port.
const configureLocalHTTPSForwarderScript = `sudo sh -s <<'PODPLANE_LOCAL_HTTPS_FORWARD_SCRIPT'
set -eu
unit=/etc/systemd/system/podplane-local-https-forward.service
if [ ! -f "${unit}" ]; then
  echo "missing ${unit}; recreate the local VM so latest user-data script can install it" >&2
  exit 1
fi
sed -i 's#TCP:[^[:space:]]*:[0-9][0-9]*#TCP:%s:%d#' "${unit}"
systemctl daemon-reload
systemctl enable --now podplane-local-https-forward.service
systemctl restart podplane-local-https-forward.service
PODPLANE_LOCAL_HTTPS_FORWARD_SCRIPT`

// Start is used to create a cluster, create a VM, and start a VM.
// Each cluster requires:
// 1.a. The package files to be downloaded to cache
// 1.b. The VM machine image to be downloaded to cache
// 2. CLI to be running a fake S3 and OIDC and package cache server in the background
// 3. The VM to be created
// 4. The VM to be started
// Start brings up the local cluster VM and writes a .cluster.jsonc config
// file describing how to log in to it. The returned path is the absolute
// location of that config (empty string on early failure paths). Callers
// (specifically `podplane local start`) use it to drive an in-process
// `podplane login --headless` against the local fake OIDC.
func (m *Local) Start(opts StartOptions) (string, error) {
	clusterID := m.clusterID
	if clusterID == "" {
		return "", fmt.Errorf("clusterID must be set")
	}
	progress := opts.Progress
	progressOutput := progress != nil
	output := io.Writer(os.Stdout)
	if progressOutput {
		output = io.Discard
	}
	m.vm.SetOutput(output)

	// Verify cached deps. If anything is missing or corrupt, auto-run a
	// download so the user doesn't need to invoke `podplane deps download`
	// before their first `local start`. After that, `local start` is
	// offline-friendly: it never makes a network call for deps as part of
	// the main flow.
	depsManager := deps.NewManager(m.depsBaseURL, m.depsCacheDir)
	kind := m.instanceKind
	arch := m.arch

	manifest, err := depsManager.Verify(kind, arch)
	if errors.Is(err, deps.ErrNotCached) || errors.Is(err, deps.ErrIncomplete) {
		download := func(progress func(deps.DownloadEvent)) error {
			return depsManager.Download(kind, arch, deps.DownloadOptions{Progress: progress, Providers: []string{"openbao"}})
		}
		if opts.RunDownloadProgress != nil {
			err = opts.RunDownloadProgress(download)
		} else {
			err = download(nil)
		}
		if err != nil {
			return "", fmt.Errorf("failed to download deps: %w", err)
		}
		manifest, err = depsManager.Verify(kind, arch)
	}
	if err != nil {
		return "", fmt.Errorf("failed to verify deps: %w", err)
	}
	registryEntries, registryReadErr := os.ReadDir(depsManager.RegistryCacheDir())
	if registryReadErr != nil || len(registryEntries) == 0 {
		download := func(progress func(deps.DownloadEvent)) error {
			return depsManager.Download(kind, arch, deps.DownloadOptions{Progress: progress, Providers: []string{"openbao"}})
		}
		if opts.RunDownloadProgress != nil {
			err = opts.RunDownloadProgress(download)
		} else {
			err = download(nil)
		}
		if err != nil {
			return "", fmt.Errorf("failed to download component image deps: %w", err)
		}
	}

	// If the cached manifest is more than 7 days old, kick off a background
	// check for newer versions. The goroutine never blocks the main flow and
	// surfaces a non-blocking note at the end of Start. Skipped entirely when
	// the manifest is fresh, so we don't pay for goroutine setup or HTTP.
	nudgeCh := make(chan string, 1)
	close(nudgeCh) // default: no nudge
	if depsManager.IsStale(kind, arch, 7*24*time.Hour) {
		nudgeCh = make(chan string, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			nudgeCh <- depsManager.CheckUpdateNudge(ctx, kind, arch)
		}()
	}

	// Start the local server as a background process if not already running
	progress.Started("server", "Local server", "")
	err = m.ServerEnsure(output)
	if err != nil {
		progress.Failed("server", "Local server", err)
		return "", fmt.Errorf("failed to start background server for local clusters: %w", err)
	}
	serverMessage := fmt.Sprintf("http %s · https %s", m.webserverPIDFile.GetData("http_port"), m.webserverPIDFile.GetData("https_port"))
	if logPath := ServerLogPath(m.runtimeDir); logPath != "" {
		serverMessage += fmt.Sprintf(" · log %s", logPath)
	}
	progress.Done("server", "Local server", serverMessage)

	// Determine the host machine address from inside the guest machine.
	hostMachineAddr := m.vm.Addr()

	// Get URLs - note that all errors after the first are the same path.
	depsServerURL, err := m.DepsServerURL(hostMachineAddr, "")
	if err != nil {
		return "", fmt.Errorf("Unexpectedly failed to get URL of server for local clusters (maybe local server isn't running yet?): %w", err)
	}
	oidcIssuerURL, err := m.OIDCServerURL(hostMachineAddr)
	if err != nil {
		return "", fmt.Errorf("unexpectedly failed to get OIDC issuer URL for local clusters: %w", err)
	}
	s3DataEndpointURL, err := m.S3DataServerURL(hostMachineAddr)
	if err != nil {
		return "", fmt.Errorf("unexpectedly failed to get local data S3 endpoint URL: %w", err)
	}
	s3CacheEndpointURL, err := m.S3CacheServerURL(hostMachineAddr)
	if err != nil {
		return "", fmt.Errorf("unexpectedly failed to get local cache S3 endpoint URL: %w", err)
	}
	nstanceRegistrationAddr := replaceAddrHost(m.webserverPIDFile.GetData("nstance_registration_addr"), hostMachineAddr)
	nstanceAgentAddr := replaceAddrHost(m.webserverPIDFile.GetData("nstance_agent_addr"), hostMachineAddr)
	if nstanceRegistrationAddr == "" || nstanceAgentAddr == "" {
		return "", fmt.Errorf("local server is missing fake nstance address metadata; stop it with `podplane local server --stop` and retry")
	}

	// Check VM exists
	vmExisted, err := m.vm.Exists()
	if err != nil {
		return "", fmt.Errorf("failed to check if VM exists: %w", err)
	}
	if m.instanceID == "" && vmExisted {
		m.instanceID = m.existingInstanceID(clusterID)
	}
	if vmExisted {
		progress.Omitted("vm-image", "VM image")
		progress.Omitted("cloud-init", "cloud-init user-data")
	}

	// Existing local clusters keep the seed recorded in cluster.jsonc. Only new
	// VMs need to resolve the requested seed version from the cached seeds
	// manifest because only new VMs write the initial Netsy snapshot.
	seed := clusterconfig.Seed{Name: seeds.None}
	if vmExisted {
		seed, err = m.SeedConfig()
		if err != nil {
			return "", err
		}
	} else {
		seedName, err := seeds.ParseName(opts.Components)
		if err != nil {
			return "", fmt.Errorf("invalid --components: %w", err)
		}
		seed = clusterconfig.Seed{Name: seedName}
		if seed.Name != seeds.None {
			seed.Version, err = depsManager.CachedSeedsVersion()
			if err != nil {
				return "", fmt.Errorf("determine Podplane seed version: %w", err)
			}
		}
	}
	if m.instanceID == "" {
		id, err := puidv7.New("knc")
		if err != nil {
			return "", fmt.Errorf("failed to generate instance ID: %w", err)
		}
		m.instanceID = id
	}
	instanceID := m.instanceID

	// Write the .cluster.jsonc stash and (on first boot) seed the local
	// Netsy bucket with the initial platform-components snapshot. Both must
	// happen before VM create/start so Netsy reads the seeded state on its
	// very first boot. The stash is also the source of truth for the
	// seeding step (it carries cluster.domains).
	hostOIDCIssuer, err := m.HostOIDCIssuerURL()
	if err != nil {
		return "", fmt.Errorf("failed to derive host OIDC issuer URL: %w", err)
	}
	apiPort, err := strconv.Atoi(m.webserverPIDFile.GetData("ingress_https_port"))
	if err != nil {
		return "", fmt.Errorf("failed to derive local Kubernetes API ingress port: %w", err)
	}
	if apiPort == 0 {
		return "", fmt.Errorf("local server is missing ingress HTTPS port")
	}
	componentsSource, err := localComponentsSource(depsManager, seed, m.vm.NodeIP())
	if err != nil {
		return "", err
	}
	stashPath, err := m.WriteLocalClusterConfig(clusterID, hostOIDCIssuer, m.OIDCCACertPath(), LocalKubernetesAPIHostname(clusterID), apiPort, seed, componentsSource)
	if err != nil {
		return "", fmt.Errorf("failed to write local cluster config: %w", err)
	}
	if !vmExisted {
		// Seed before VM creation so a later create failure doesn't leave
		// us in a state where vmExisted=true skips seeding on retry.
		depsServerURLHost, err := m.DepsServerURL("", "")
		if err != nil {
			return "", fmt.Errorf("failed to derive host-side deps URL for seeding: %w", err)
		}
		if err := m.ensureInitialNetsySnapshot(stashPath, depsServerURLHost, seed); err != nil {
			return "", fmt.Errorf("seed local Netsy snapshot: %w", err)
		}
		// Create the VM, using the cached OS image from the vmconfig manifest as
		// the qcow2 backing file.
		baseImage := depsManager.VMConfigArtifactCachePath(deps.ImageDepName, manifest.VMConfig.OS.Image)
		progress.Started("vm-image", "VM image", "")
		if err := m.vm.Create(baseImage); err != nil {
			progress.Failed("vm-image", "VM image", err)
			_ = m.ServerCleanup()
			return "", fmt.Errorf("failed to create VM: %w", err)
		}
		progress.Done("vm-image", "VM image", "")
	}

	// Prefer direct kernel boot when the manifest provides explicit boot
	// metadata. If extraction fails, fall back to firmware/GRUB boot.
	var directBoot *vm.DirectBootOptions
	image := manifest.VMConfig.OS.Image
	boot := manifest.VMConfig.OS.Boot
	if boot.Complete() {
		imagePath := depsManager.VMConfigArtifactCachePath(deps.ImageDepName, image)
		directBoot, err = osboot.Prepare(osboot.Options{
			ImagePath: imagePath,
			CacheDir:  filepath.Join(filepath.Dir(imagePath), "boot"),
			Boot:      boot,
		})
		if err != nil {
			fmt.Printf("Direct boot unavailable, falling back to firmware boot: %v\n", err)
		}
	}

	// Get one key for ssh authorized_keys file.
	sshAuthorizedKey, err := SSHPublicKey(m.dataDir)
	if err != nil {
		return "", fmt.Errorf("failed to prepare SSH key for local VM: %w", err)
	}

	// Read and base64-encode the local OIDC CA certificate.
	oidcCACertPath := m.OIDCCACertPath()
	certBytes, err := os.ReadFile(oidcCACertPath)
	if err != nil {
		return "", fmt.Errorf("failed to read local OIDC CA certificate file %s: %w", oidcCACertPath, err)
	}
	encodedCACert := base64.StdEncoding.EncodeToString(certBytes)

	// Configure the local fake Nstance deployment before rendering user-data. The
	// background `podplane local server` process owns the listening gRPC services;
	// this call opens the same durable store to write tenant config and bootstrap
	// state idempotently.
	nstanceBootstrap, err := configureLocalNstance(
		context.Background(),
		m.dataDir,
		clusterID,
		instanceID,
		kind,
		nstanceRegistrationAddr,
		nstanceAgentAddr,
		hostMachineAddr,
	)
	if err != nil {
		return "", fmt.Errorf("failed to configure local fake nstance: %w", err)
	}
	nstanceStore, err := newLocalNstanceStore(filepath.Join(m.dataDir, "nstance-fake"))
	if err != nil {
		return "", fmt.Errorf("failed to initialize local fake nstance store: %w", err)
	}

	// Render the user-data script.
	vars := userdata.TemplateVars{
		Manifest:      manifest,
		DepsMirrorURL: depsServerURL,
		Cluster: userdata.ClusterData{
			ID:     clusterID,
			CACert: nstanceBootstrap.CACert,
		},
		Provider: userdata.ProviderData{
			Kind:   "local",
			Region: "local",
			Zone:   "local",
		},
		Instance: userdata.InstanceData{
			ID:   instanceID,
			Type: "local",
		},
		Server: userdata.ServerData{
			RegistrationAddr: nstanceBootstrap.ServerRegistrationAddr,
			AgentAddr:        nstanceBootstrap.ServerAgentAddr,
		},
		Local: userdata.LocalData{
			VMForwardPortToLocalServerHTTPS: localVMForwardPortToLocalServerHTTPS,
			LocalServerHostFromVM:           m.vm.Addr(),
			LocalServerHTTPSPort:            m.webserverPIDFile.GetData("https_port"),
		},
		Vars: userdata.MutableVars{
			"SSH_AUTHORIZED_KEY":       sshAuthorizedKey,
			"OIDC_ISSUER":              oidcIssuerURL,
			"OIDC_CUSTOM_CA":           encodedCACert,
			"OIDC_CA_FILE":             "/opt/crt/oidc-ca.pem",
			"KUBE_LOG_LEVEL":           "5",
			"KUBE_API_PUBLIC_HOSTNAME": "localhost",
			"KUBE_API_ETCD_SERVERS":    "https://127.0.0.1:2378",
			"TELEMETRY_LOG_SERVICES":   "first-boot-env,cron,ssh,netsy,nstance-agent,nstance-recv-watch,containerd,kube-apiserver,kube-controller-manager,kube-scheduler,kubelet,zot",
			"REGISTRY_ENABLED":         "true",
			"REGISTRY_HOSTNAME":        localRegistryHostname(clusterID),
			"REGISTRY_BUCKET":          "registry",
		},
		Nonce: nstanceBootstrap.RegistrationNonceJWT,
	}
	vars.Vars.SetObjectStorageEndpoint(s3DataEndpointURL)
	vars.Vars["REGISTRY_ENDPOINT"] = s3CacheEndpointURL
	vars.Vars.SetObjectStorageRegion("local")
	vars.Vars["NETSY_ACCESS_KEY_ID"] = "test"
	vars.Vars["NETSY_SECRET_ACCESS_KEY"] = "test"
	vars.Vars["TELEMETRY_S3_ACCESS_KEY_ID"] = "test"
	vars.Vars["TELEMETRY_S3_SECRET_ACCESS_KEY"] = "test"
	vars.Vars["REGISTRY_ACCESS_KEY_ID"] = "test"
	vars.Vars["REGISTRY_SECRET_ACCESS_KEY"] = "test"
	vars.Vars["AWS_S3_USE_PATH_STYLE"] = "true"
	vars.Vars["NSTANCE_SERVER_REGISTRATION_ADDR"] = nstanceBootstrap.ServerRegistrationAddr
	vars.Vars["NSTANCE_SERVER_AGENT_ADDR"] = nstanceBootstrap.ServerAgentAddr
	vars.ApplyDefaults()
	mutableEnv := userdata.RenderMutableEnv(vars.Vars)
	mutableEnvChanged := false
	if vmExisted {
		mutableEnvChanged, err = m.stageMutableEnvIfChanged(context.Background(), nstanceStore, clusterID, instanceID, mutableEnv)
		if err != nil {
			return "", fmt.Errorf("failed to stage local mutable env update: %w", err)
		}
	}
	rendered, err := vars.Render()
	if err != nil {
		return "", fmt.Errorf("failed to render userdata: %w", err)
	}
	userdataFile := m.UserdataPath(clusterID)
	if err := os.MkdirAll(m.UserdataDir(clusterID), 0755); err != nil {
		return "", fmt.Errorf("failed to create userdata directory: %w", err)
	}
	if err := os.WriteFile(userdataFile, []byte(rendered), 0644); err != nil {
		return "", fmt.Errorf("failed to write user-data file %s: %w", userdataFile, err)
	}

	// Select host ports for the VM before starting it. Defaults are preferred for
	// single-VM local clusters, but occupied ports fall back to available dynamic
	// ports so multiple local VMs can run at the same time.
	vmPortForwards, vmPorts, err := allocateLocalVMPorts()
	if err != nil {
		return "", err
	}

	// Start the VM
	progress.Started("vm", "VM", fmt.Sprintf("qemu · %s CPU · %s", opts.CPUs, opts.Memory))
	if err := m.vm.Start(rendered, opts.CPUs, opts.Memory, sshAuthorizedKey, false, directBoot, vmPortForwards); err != nil {
		progress.Failed("vm", "VM", err)
		if !errors.Is(err, vm.ErrAlreadyRunning) {
			_ = m.ServerCleanup()
		}
		return "", fmt.Errorf("failed to start VM: %w", err)
	}
	progress.Done("vm", "VM", "")
	if !vmExisted {
		if err := m.writeMutableEnvBaseline(clusterID, mutableEnv); err != nil {
			return "", fmt.Errorf("failed to record local mutable env baseline: %w", err)
		}
	}
	if err := writeState(m.runtimeDir, clusterState{
		ClusterID: clusterID,
		Backend:   "qemu",
		Ports:     vmPorts,
	}); err != nil {
		return "", err
	}

	if !progressOutput {
		color.Green("✓ VM started successfully")
	}
	if !vmExisted {
		progress.Started("cloud-init", "cloud-init user-data", "")
		if err := m.WaitForReadiness(context.Background(), ReadinessOptions{
			StreamUserdataLogs: opts.StreamUserdataLogs,
			Quiet:              progressOutput,
		}); err != nil {
			progress.Failed("cloud-init", "cloud-init user-data", err)
			return "", fmt.Errorf("local VM readiness check failed: %w", err)
		}
		progress.Done("cloud-init", "cloud-init user-data", "")
	}
	progress.Started("system-services", "systemd services", "")
	if err := m.WaitForSystemServices(context.Background(), WaitOptions{Quiet: progressOutput}); err != nil {
		progress.Failed("system-services", "system services", err)
		return "", fmt.Errorf("local VM system service readiness check failed: %w", err)
	}
	progress.Done("system-services", "system services", "")
	progress.Started("https-forward", "local HTTPS server forwarder", "")
	if err := m.configureLocalHTTPSForwarder(context.Background(), vmPorts.SSH, m.webserverPIDFile.GetData("https_port")); err != nil {
		progress.Failed("https-forward", "local HTTPS server forwarder", err)
		return "", fmt.Errorf("failed to configure local HTTPS server forwarder: %w", err)
	}
	progress.Done("https-forward", "local HTTPS server forwarder", fmt.Sprintf("https://%s:%d", m.vm.NodeIP(), localVMForwardPortToLocalServerHTTPS))
	if vmExisted && mutableEnvChanged {
		if err := m.repairExistingNstanceAgentEnv(context.Background(), vmPorts.SSH, nstanceBootstrap.ServerRegistrationAddr, nstanceBootstrap.ServerAgentAddr, progressOutput); err != nil {
			return "", fmt.Errorf("failed to repair local nstance agent endpoint config: %w", err)
		}
	}
	progress.Started("nstance-agent", "nstance", "registration")
	if err := m.WaitForNstanceAgentRegistration(context.Background(), WaitOptions{Quiet: progressOutput}); err != nil {
		progress.Failed("nstance-agent", "Nstance agent", err)
		return "", fmt.Errorf("local VM nstance-agent readiness check failed: %w", err)
	}
	progress.Done("nstance-agent", "Nstance agent", "")
	progress.Started("netsy", "netsy", "health")
	if err := m.WaitForNetsyHealth(context.Background(), WaitOptions{Quiet: progressOutput}); err != nil {
		progress.Failed("netsy", "Netsy", err)
		return "", fmt.Errorf("local VM Netsy health check failed: %w", err)
	}
	progress.Done("netsy", "Netsy", "")
	progress.Started("api-live", "kubernetes live", "")
	if err := m.waitForAPIServerEndpoint(context.Background(), "live", progressOutput, m.ProbeAPIServerLive); err != nil {
		progress.Failed("api-live", "Kubernetes API live", err)
		return "", fmt.Errorf("local VM Kubernetes API readiness check failed: %w", err)
	}
	progress.Done("api-live", "Kubernetes API live", "")
	progress.Started("api-ready", "kubernetes ready", "")
	if err := m.waitForAPIServerEndpoint(context.Background(), "ready", progressOutput, m.ProbeAPIServerReady); err != nil {
		progress.Failed("api-ready", "Kubernetes API ready", err)
		return "", fmt.Errorf("local VM Kubernetes API readiness check failed: %w", err)
	}
	progress.Done("api-ready", "Kubernetes API ready", "")

	// Print any pending update nudge. Non-blocking: if the goroutine hasn't
	// produced a result yet (e.g. fast-fail before its 2s timeout), we drop
	// the message rather than delay return.
	select {
	case msg := <-nudgeCh:
		if msg != "" {
			fmt.Println(msg)
		}
	default:
	}

	return stashPath, nil
}

// repairExistingNstanceAgentEnv updates only nstance-agent env for existing VMs
// if the fake nstance-server ports have changed, enabling the agent to reconnect
// and receive a mutable.env update to fix ports for all other services.
func (m *Local) repairExistingNstanceAgentEnv(ctx context.Context, sshPort int, registrationAddr, agentAddr string, quiet bool) error {
	if err := m.WaitForReadiness(ctx, ReadinessOptions{Quiet: quiet}); err != nil {
		return err
	}
	registrationExpr := "s|^NSTANCE_SERVER_REGISTRATION_ADDR=.*|NSTANCE_SERVER_REGISTRATION_ADDR=" + userdata.QuoteEnvValue(registrationAddr) + "|"
	agentExpr := "s|^NSTANCE_SERVER_AGENT_ADDR=.*|NSTANCE_SERVER_AGENT_ADDR=" + userdata.QuoteEnvValue(agentAddr) + "|"
	command := fmt.Sprintf(
		"sudo sed -i -e %s -e %s /opt/env/nstance-agent.env && sudo systemctl restart nstance-agent",
		userdata.QuoteEnvValue(registrationExpr),
		userdata.QuoteEnvValue(agentExpr),
	)
	privateKeyPath, err := SSHPrivateKeyPath(m.dataDir)
	if err != nil {
		return fmt.Errorf("failed to prepare SSH key for local VM: %w", err)
	}
	output, err := m.vm.Shell(ctx, command, sshPort, privateKeyPath, vm.ShellOptions{Timeout: 30 * time.Second})
	if err != nil {
		return fmt.Errorf("restart nstance-agent with current endpoints: %w: %s", err, string(output))
	}
	return nil
}

// configureLocalHTTPSForwarder refreshes the VM-local HTTPS TCP forwarder.
func (m *Local) configureLocalHTTPSForwarder(ctx context.Context, sshPort int, httpsPort string) error {
	if httpsPort == "" {
		return fmt.Errorf("local server HTTPS port is required")
	}
	parsedHTTPSPort, err := strconv.Atoi(httpsPort)
	if err != nil || parsedHTTPSPort <= 0 {
		return fmt.Errorf("local server HTTPS port %q is invalid", httpsPort)
	}
	privateKeyPath, err := SSHPrivateKeyPath(m.dataDir)
	if err != nil {
		return fmt.Errorf("failed to prepare SSH key for local VM: %w", err)
	}
	command := fmt.Sprintf(configureLocalHTTPSForwarderScript, m.vm.Addr(), parsedHTTPSPort)
	output, err := m.vm.Shell(ctx, command, sshPort, privateKeyPath, vm.ShellOptions{Timeout: 30 * time.Second})
	if err != nil {
		return fmt.Errorf("configure podplane-local-https-forward.service: %w: %s", err, string(output))
	}
	return nil
}

var localUserdataInstanceIDPattern = regexp.MustCompile(`(?m)^INSTANCE_ID='([^']+)'$`)

// existingInstanceID returns the instance ID from the existing VM's user-data.
func (m *Local) existingInstanceID(clusterID string) string {
	data, err := os.ReadFile(m.UserdataPath(clusterID))
	if err != nil {
		return ""
	}
	match := localUserdataInstanceIDPattern.FindSubmatch(data)
	if len(match) != 2 {
		return ""
	}
	return string(match[1])
}

// SeedConfig returns cluster.seed from this local cluster's config.
func (m *Local) SeedConfig() (clusterconfig.Seed, error) {
	if m.clusterID == "" {
		return clusterconfig.Seed{}, fmt.Errorf("clusterID must be set")
	}
	clusterID := m.clusterID
	path := ClusterConfigPath(m.dataDir, clusterID)
	cfg, err := clusterconfig.Load(path)
	if err != nil {
		return clusterconfig.Seed{}, fmt.Errorf("read existing local cluster config: %w", err)
	}
	seed := cfg.Cluster.Seed
	if seed.Name == "" {
		return clusterconfig.Seed{Name: seeds.None}, nil
	}
	seed.Name, err = seeds.ParseName(seed.Name)
	if err != nil {
		return clusterconfig.Seed{}, fmt.Errorf("read existing cluster.seed.name: %w", err)
	}
	if seed.Name != seeds.None && seed.Version == "" {
		return clusterconfig.Seed{}, fmt.Errorf("read existing cluster.seed.version: is required")
	}
	return seed, nil
}

// WriteLocalClusterConfig writes a JSONC cluster config to
// <dataDir>/local/<clusterID>/cluster.jsonc and returns its absolute path. It
// describes how the host CLI can reach the local cluster's OIDC issuer and
// (eventually) Kubernetes API.
func (m *Local) WriteLocalClusterConfig(clusterID, oidcIssuerURL, oidcCACertPath, apiHostname string, apiPort int, seed clusterconfig.Seed, componentsSource *clusterconfig.ComponentsSource) (string, error) {
	dir := ClusterDataDir(m.dataDir, clusterID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	var err error
	seed.Name, err = seeds.ParseName(seed.Name)
	if err != nil {
		return "", fmt.Errorf("invalid cluster.seed.name: %w", err)
	}
	if seed.Name == seeds.None {
		seed.Version = ""
	} else if seed.Version == "" {
		return "", fmt.Errorf("invalid cluster.seed.version: is required")
	}
	seedBlock := `    "seed": {},
`
	if seed.Name != seeds.None {
		seedBlock = fmt.Sprintf(`    "seed": {
      "name": %q,
      "version": %q
    },
`, seed.Name, seed.Version)
	}
	componentsSourceBlock := ""
	if componentsSource != nil {
		refKey := "tag"
		refValue := componentsSource.Ref.Tag
		if componentsSource.Ref.Branch != "" {
			refKey = "branch"
			refValue = componentsSource.Ref.Branch
		} else if componentsSource.Ref.Commit != "" {
			refKey = "commit"
			refValue = componentsSource.Ref.Commit
		}
		componentsSourceBlock = fmt.Sprintf(`      "source": {
        "url": %q,
        "ref": {
          %q: %q
        }
      },
`, componentsSource.URL, refKey, refValue)
	}
	registryHostname := localRegistryHostname(clusterID)
	vaultAddress := m.localVaultServerURLForConfig(clusterID)
	path := ClusterConfigPath(m.dataDir, clusterID)
	contents := fmt.Sprintf(`{
  // Auto-generated by `+"`"+`podplane local start`+"`"+` — describes the local
  // cluster so that `+"`"+`podplane login --cluster %s`+"`"+` (and the kubectl auth
  // hook) can find it.
  "cluster": {
    "id": %q,
    "name": %q,
    "oidc": {
      "issuer_url": %q,
      "client_id": %q,
      "username_claim": "email",
      "ca_cert": %q,
      "signing_algs": ["RS256"]
    },
    "domains": [
      {
        "zone": %q,
        "provider": { "kind": "local" }
      }
    ],
    "secrets": {
      "default_provider": "local-fakevault",
      "providers": {
        "local-fakevault": {
          "kind": "openbao",
          "address": %q,
          "mount_path": "secret"
        }
      }
    },
    "kubernetes": {
      "api_hostname": %q,
      "api_port": %d
    },
%s
    "components": {
%s
      "registry": {
        "mirror": {
          "enabled": true,
          "hostname": %q
        }
      }
    }
  }
}
`, clusterID, clusterID, "local-"+clusterID, oidcIssuerURL, clusterID, oidcCACertPath, clusterID+".localhost", vaultAddress, apiHostname, apiPort, seedBlock, componentsSourceBlock, registryHostname)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

func (m *Local) localVaultServerURLForConfig(clusterID string) string {
	address, err := m.VaultServerURL(localGuestHostAddr)
	if err == nil {
		return address
	}
	// Unit tests generate local cluster configs without a running local server
	// PID file. Real `local start` has already started the server before this
	// point, so this fallback is only for offline config rendering.
	return fmt.Sprintf("https://%s:0/vault/%s", localGuestHostAddr, clusterID)
}

// localComponentsSource returns the components Git source pinned to the cached
// components manifest version used by local seeded clusters. Cached local Git
// repositories are preferred so local VMs can reconcile without reaching GitHub.
func localComponentsSource(depsManager *deps.Manager, seed clusterconfig.Seed, nodeIP string) (*clusterconfig.ComponentsSource, error) {
	if seed.Name == seeds.None {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(depsManager.GitCachePath("components.git"), "config")); err == nil {
		return &clusterconfig.ComponentsSource{
			URL: localGitURL(nodeIP, "components.git"),
			Ref: clusterconfig.ComponentsSourceRef{Branch: "local-dev"},
		}, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect local components git cache: %w", err)
	}
	manifest, err := depsManager.CachedComponentsManifest()
	if err != nil {
		return nil, fmt.Errorf("determine Podplane components version: %w", err)
	}
	if manifest.Components.Source != nil {
		source := manifest.Components.Source
		if repoPath, err := deps.GitCacheRepoPath(source.URL); err == nil {
			if _, statErr := os.Stat(filepath.Join(depsManager.GitCachePath(repoPath), "config")); statErr == nil {
				return &clusterconfig.ComponentsSource{
					URL: localGitURL(nodeIP, repoPath),
					Ref: clusterconfig.ComponentsSourceRef{Branch: source.Ref.Branch, Tag: source.Ref.Tag, Commit: source.Ref.Commit},
				}, nil
			} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return nil, fmt.Errorf("inspect components git cache: %w", statErr)
			}
		}
		return &clusterconfig.ComponentsSource{
			URL: source.URL,
			Ref: clusterconfig.ComponentsSourceRef{Branch: source.Ref.Branch, Tag: source.Ref.Tag, Commit: source.Ref.Commit},
		}, nil
	}
	if manifest.Components.Version == "dev" {
		return &clusterconfig.ComponentsSource{
			URL: "https://github.com/podplane/components.git",
			Ref: clusterconfig.ComponentsSourceRef{Branch: "main"},
		}, nil
	}
	if manifest.Components.Version == "" {
		return nil, fmt.Errorf("cached components manifest is missing version")
	}
	return &clusterconfig.ComponentsSource{
		URL: "https://github.com/podplane/components.git",
		Ref: clusterconfig.ComponentsSourceRef{Tag: "v" + manifest.Components.Version},
	}, nil
}

func localRegistryHostname(clusterID string) string {
	return fmt.Sprintf("%s-registry.local", clusterID)
}

func replaceAddrHost(addr, host string) string {
	if addr == "" || host == "" {
		return addr
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return net.JoinHostPort(host, port)
}

// HostOIDCIssuerURL returns the OIDC issuer URL as reachable from the host
// machine (where the CLI itself runs), not from inside the guest VM.
func (m *Local) HostOIDCIssuerURL() (string, error) {
	port, err := m.LocalServerHTTPSPort()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://%s:%s/oidc", localOIDCHostname, port), nil
}
