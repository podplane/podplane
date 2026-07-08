// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package kubectl

import (
	"bytes"
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
	// run kubectl to check if credentials already exists
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cmd := execwrap.Command(
		"kubectl",
		"config",
		"view",
		"--output=jsonpath={.users[?(@.name==\""+key+"\")].user.exec.command}",
	)
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		stderr := strings.TrimSpace(errBuf.String())
		// Handle the specific JSONPath error when users array is nil (first-time setup)
		if strings.Contains(stderr, "is not array or slice and cannot be filtered") {
			// This is expected for first-time setup, continue with credential configuration
		} else {
			return fmt.Errorf("Error invoking kubectl command: %s", err)
		}
	} else {
		outString := strings.TrimSpace(outBuf.String())
		if outString == cliBinaryPath {
			_, _ = fmt.Fprintf(stdout, "Credentials already exist for %s\n", key)
			return nil
		} else if outString != "" && !strings.Contains(outString, "podplane") {
			_, _ = fmt.Fprintf(stdout, "Skipping configuration of kubectl credentials '%s' due to unknown conflict.\n", key)
			return nil
		}
	}
	// run kubectl to configure credentials
	cmd = execwrap.Command(
		"kubectl",
		"config",
		"set-credentials",
		key,
		"--exec-api-version=client.authentication.k8s.io/v1beta1",
		"--exec-command="+cliBinaryPath,
		"--exec-arg=hooks",
		"--exec-arg=kubectl-auth",
		"--exec-arg=--cluster",
		"--exec-arg="+clusterID,
		"--exec-arg=--user",
		"--exec-arg="+sub,
	)
	if local {
		cmd.Args = append(cmd.Args, "--exec-env="+config.KeyringPassEnv+"="+config.LocalKeyringPass)
	}
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
