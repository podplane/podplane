// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/podplane/podplane/internal/execwrap"
)

// Sync rsync's files into the running local cluster qemu VM.
func (m *Qemu) Sync(from, to, chown string, excludes []string, sshPort int, identityFile string) error {
	// Check if VM is running
	running, err := m.Running()
	if err != nil {
		return err
	}
	if !running {
		return fmt.Errorf("no VM is currently running")
	}
	chownFlag := ""
	if chown != "" {
		chownFlag = fmt.Sprintf("--chown=%s", chown)
	}
	excludeFlags := make([]string, 0, len(excludes)*2)
	for _, exclude := range excludes {
		if exclude == "" {
			continue
		}
		excludeFlags = append(excludeFlags, "--exclude", exclude)
	}

	// build SSH arguments
	sshArgs := []string{
		// configure port
		"-p", strconv.Itoa(sshPort),
		// trust any server key, prevent prompts and warnings when connecting
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
	}
	// add identity (public key) file if set
	if identityFile != "" {
		sshArgs = append(sshArgs, "-i", identityFile)
	}

	// Run rsync into the VM
	args := []string{
		"rsync",
		//
		"-av",
		// configure ssh options ....
		"-e", `ssh ` + strings.Join(sshArgs, " "),
	}
	if chownFlag != "" {
		args = append(args, chownFlag)
	}
	args = append(args, excludeFlags...)
	args = append(args,
		// run as root on target
		`--rsync-path`, `sudo rsync`,
		// local path
		from,
		// set user and host and target path
		"debian@127.0.0.1:"+to,
	)
	cmd := execwrap.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
