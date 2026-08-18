// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package kubectl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/execwrap"
)

// SetCredentials first uses kubectl to check if a credential with the same key
// already exists (and early exits if so), then runs kubectl to set credentials.
//
// The credentials key is `podplane-<clusterID>-<sub>`. The exec plugin invokes
// `podplane hooks kubectl-auth --cluster <clusterID> --user <sub>`.
func SetCredentials(stdout io.Writer, sub string, clusterID string, local bool) error {
	// get path of current binary
	cliBinaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get binary path: %w", err)
	}
	goRunPath := string(filepath.Separator) + "go-build"
	goRunExePath := string(filepath.Separator) + "exe" + string(filepath.Separator)
	if strings.Contains(cliBinaryPath, goRunPath) && strings.Contains(cliBinaryPath, goRunExePath) {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve stable kubectl exec path: %w", err)
		}
		cliBinaryPath = filepath.Join(cwd, "bin", filepath.Base(cliBinaryPath))
	}
	// build kubectl credentials key
	key := CredentialsKey(sub, clusterID, local)
	// Read JSON and compare names in Go. Credential names are untrusted input
	// and must never be interpolated into kubectl JSONPath expressions.
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cmd := execwrap.Command(
		"kubectl",
		"config",
		"view",
		"--output=json",
	)
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error invoking kubectl command: %s", err)
	} else {
		command, found, err := credentialExecCommand(outBuf.Bytes(), key)
		if err != nil {
			return fmt.Errorf("parse kubectl config: %w", err)
		}
		if found && command == cliBinaryPath {
			_, _ = fmt.Fprintf(stdout, "Credentials already exist for %s\n", key)
			return nil
		} else if found && !strings.Contains(command, "podplane") {
			_, _ = fmt.Fprintf(stdout, "Skipping configuration of kubectl credentials '%s' due to unknown conflict.\n", key)
			return nil
		}
	}
	// run kubectl to configure credentials
	args := credentialExecArgs(cliBinaryPath, sub, clusterID, local)
	cmd = execwrap.Command(
		"kubectl",
		args...,
	)
	if local {
		cmd.Args = append(cmd.Args, "--exec-env="+config.KeyringPassEnv+"="+config.LocalKeyringPass)
	}
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// credentialExecArgs returns kubectl arguments for installing the auth hook.
func credentialExecArgs(cliBinaryPath, sub, clusterID string, local bool) []string {
	args := []string{
		"config",
		"set-credentials",
		CredentialsKey(sub, clusterID, local),
		"--exec-api-version=client.authentication.k8s.io/v1beta1",
		"--exec-command=" + cliBinaryPath,
		"--exec-arg=hooks",
		"--exec-arg=kubectl-auth",
		"--exec-arg=--cluster",
		"--exec-arg=" + clusterID,
		"--exec-arg=--user",
		"--exec-arg=" + sub,
	}
	if local {
		args = append(args, "--exec-arg=--local")
	}
	return args
}

// credentialExecCommand finds the exec command for an exact kubeconfig user name.
func credentialExecCommand(data []byte, name string) (string, bool, error) {
	var cfg struct {
		Users []struct {
			Name string `json:"name"`
			User struct {
				Exec *struct {
					Command string `json:"command"`
				} `json:"exec"`
			} `json:"user"`
		} `json:"users"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", false, err
	}
	for _, user := range cfg.Users {
		if user.Name == name {
			if user.User.Exec == nil {
				return "", true, nil
			}
			return user.User.Exec.Command, true, nil
		}
	}
	return "", false, nil
}
