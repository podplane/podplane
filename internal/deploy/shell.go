// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/podplane/podplane/internal/execwrap"
)

// ConfirmDebugFunc asks whether to start an ephemeral debug container.
type ConfirmDebugFunc func(message string) (bool, error)

// ConfirmRestartFunc asks whether to restart an app workload.
type ConfirmRestartFunc func(message string) (bool, error)

// ShellOptions controls shell access for a deployed app.
type ShellOptions struct {
	Name            string
	Namespace       string
	Context         string
	Kubeconfig      string
	Container       string
	Command         []string
	SelectContainer SelectContainerFunc
	ShellPrompt     bool
	NoDebug         bool
	DebugImage      string
	ConfirmDebug    ConfirmDebugFunc
	ConfirmRestart  ConfirmRestartFunc
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
}

// Shell opens an app shell or runs one command in the selected app container.
func Shell(opts ShellOptions) error {
	target := AppTargetOptions{
		Name:            opts.Name,
		Namespace:       opts.Namespace,
		Context:         opts.Context,
		Kubeconfig:      opts.Kubeconfig,
		Container:       opts.Container,
		SelectContainer: opts.SelectContainer,
	}
	pod, container, err := appTarget(target, "shell")
	if err != nil {
		return err
	}
	if len(opts.Command) > 0 {
		return runKubectlExec(target, pod, container, false, opts.Command, opts.Stdin, opts.Stdout, opts.Stderr)
	}
	if shellAvailable(target, pod, container, []string{"bash", "-lc", "exit"}) {
		return runKubectlExec(target, pod, container, true, []string{"bash", "-l"}, opts.Stdin, opts.Stdout, opts.Stderr)
	}
	if shellAvailable(target, pod, container, []string{"sh", "-c", "exit"}) {
		return runKubectlExec(target, pod, container, true, []string{"sh"}, opts.Stdin, opts.Stdout, opts.Stderr)
	}
	if !opts.ShellPrompt {
		return fmt.Errorf("container %q has no usable bash or sh; run a one-off command with `podplane shell %s -- <command>`", container, opts.Name)
	}
	if !opts.NoDebug && opts.ConfirmDebug != nil {
		workload, err := debugRestartWorkload(target, pod)
		if err != nil {
			_, _ = fmt.Fprintf(writerOrDefault(opts.Stderr, os.Stderr), "debug container unavailable: %v\nFalling back to shell prompt.\n", err)
		} else if ok, err := opts.ConfirmDebug(fmt.Sprintf("Container %q has no bash or sh.\nStart an ephemeral debug container in pod %q?", container, pod)); err != nil {
			return err
		} else if ok {
			if err := runDebugShell(opts, target, pod, container); err == nil {
				return maybeRestartDebugWorkload(opts, target, workload)
			} else {
				_, _ = fmt.Fprintf(writerOrDefault(opts.Stderr, os.Stderr), "debug container failed: %v\nFalling back to shell prompt.\n", err)
			}
		}
	}
	envSupported := shellPromptEnvSupported(target, pod, container)
	return runShellPrompt(opts, target, pod, container, envSupported)
}

// maybeRestartDebugWorkload offers to restart a workload after debug exits.
func maybeRestartDebugWorkload(opts ShellOptions, target AppTargetOptions, workload appWorkloadRef) error {
	if opts.ConfirmRestart == nil {
		return nil
	}
	ok, err := opts.ConfirmRestart(fmt.Sprintf("The debug container remains until the pod is recreated.\nRestart %s/%s now to remove it? This will briefly interrupt app traffic.", workload.Kind, workload.Name))
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return rolloutRestart(target, workload)
}

// debugRestartWorkload returns the safe workload to restart after debugging.
func debugRestartWorkload(target AppTargetOptions, pod string) (appWorkloadRef, error) {
	podInfo, err := readPodOwner(target, pod)
	if err != nil {
		return appWorkloadRef{}, err
	}
	owner := controllerOwner(podInfo.Metadata.OwnerReferences)
	if owner.Kind == "" {
		return appWorkloadRef{}, fmt.Errorf("pod %q has no controller owner", pod)
	}
	switch owner.Kind {
	case "ReplicaSet":
		return deploymentForReplicaSet(target, owner.Name)
	case "StatefulSet", "DaemonSet":
		return appWorkloadRef{Kind: strings.ToLower(owner.Kind), Name: owner.Name}, nil
	default:
		return appWorkloadRef{}, fmt.Errorf("pod %q is managed by %s/%s, not a Deployment, StatefulSet, or DaemonSet", pod, owner.Kind, owner.Name)
	}
}

// rolloutRestart restarts a workload with kubectl rollout restart.
func rolloutRestart(target AppTargetOptions, workload appWorkloadRef) error {
	args := kubectlContextArgs(target)
	args = append(args, "rollout", "restart", workload.Kind+"/"+workload.Name)
	cmd := execwrap.Command("kubectl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// appWorkloadRef identifies a restartable Kubernetes workload.
type appWorkloadRef struct {
	Kind string
	Name string
}

// ownerRef is a Kubernetes ownerReference subset.
type ownerRef struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Controller *bool  `json:"controller"`
}

// podOwnerInfo is the pod/replicaset JSON subset used for owner resolution.
type podOwnerInfo struct {
	Metadata struct {
		OwnerReferences []ownerRef `json:"ownerReferences"`
	} `json:"metadata"`
}

// readPodOwner reads owner references for a pod.
func readPodOwner(target AppTargetOptions, pod string) (podOwnerInfo, error) {
	args := kubectlContextArgs(target)
	args = append(args, "get", "pod", pod, "-o", "json")
	cmd := execwrap.Command("kubectl", args...)
	out, err := cmd.Output()
	if err != nil {
		return podOwnerInfo{}, fmt.Errorf("read pod owner: %w", err)
	}
	var info podOwnerInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return podOwnerInfo{}, fmt.Errorf("parse pod owner: %w", err)
	}
	return info, nil
}

// deploymentForReplicaSet returns the Deployment owning a ReplicaSet.
func deploymentForReplicaSet(target AppTargetOptions, name string) (appWorkloadRef, error) {
	args := kubectlContextArgs(target)
	args = append(args, "get", "replicaset", name, "-o", "json")
	cmd := execwrap.Command("kubectl", args...)
	out, err := cmd.Output()
	if err != nil {
		return appWorkloadRef{}, fmt.Errorf("read replicaset owner: %w", err)
	}
	var info podOwnerInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return appWorkloadRef{}, fmt.Errorf("parse replicaset owner: %w", err)
	}
	owner := controllerOwner(info.Metadata.OwnerReferences)
	if owner.Kind != "Deployment" || owner.Name == "" {
		return appWorkloadRef{}, fmt.Errorf("replicaset %q is not owned by a Deployment", name)
	}
	return appWorkloadRef{Kind: "deployment", Name: owner.Name}, nil
}

// controllerOwner returns the controller owner reference.
func controllerOwner(owners []ownerRef) ownerRef {
	for _, owner := range owners {
		if owner.Controller != nil && *owner.Controller {
			return owner
		}
	}
	return ownerRef{}
}

// shellAvailable quietly probes whether a shell command can start.
func shellAvailable(target AppTargetOptions, pod string, container string, command []string) bool {
	return runKubectlExec(target, pod, container, false, command, strings.NewReader(""), io.Discard, io.Discard) == nil
}

// shellArgs builds kubectl exec args for an app shell command.
func shellArgs(target AppTargetOptions, pod string, container string, tty bool, command []string) []string {
	args := kubectlContextArgs(target)
	args = append(args, "exec", "-i")
	if tty {
		args = append(args, "-t")
	}
	args = append(args, pod, "--container", container, "--")
	args = append(args, command...)
	return args
}

// debugArgs builds kubectl debug args for a shell in an ephemeral container.
func debugArgs(target AppTargetOptions, pod string, targetContainer string, debugContainer string, image string) []string {
	args := kubectlContextArgs(target)
	args = append(args,
		"debug", pod,
		"-i", "-t",
		"--quiet",
		"--profile", "general",
		"--image", image,
		"--target", targetContainer,
		"--container", debugContainer,
		"--", "sh",
	)
	return args
}

// runDebugShell starts a shell in an ephemeral debug container attached to the app pod.
func runDebugShell(opts ShellOptions, target AppTargetOptions, pod string, targetContainer string) error {
	image := opts.DebugImage
	if image == "" {
		image = "busybox:latest"
	}
	debugID := fmt.Sprintf("dbg-%d", time.Now().UnixNano())
	debugContainer := "podplane-" + debugID
	if err := markDebugPod(target, pod, debugID, debugContainer, targetContainer, time.Now().UTC()); err != nil {
		return err
	}
	cmd := execwrap.Command("kubectl", debugArgs(target, pod, targetContainer, debugContainer, image)...)
	cmd.Stdin = readerOrDefault(opts.Stdin, os.Stdin)
	cmd.Stdout = writerOrDefault(opts.Stdout, os.Stdout)
	cmd.Stderr = writerOrDefault(opts.Stderr, os.Stderr)
	return cmd.Run()
}

// markDebugPod marks pods that received Podplane shell debug containers.
func markDebugPod(target AppTargetOptions, pod string, debugID string, debugContainer string, targetContainer string, createdAt time.Time) error {
	cmd := execwrap.Command("kubectl", markDebugPodArgs(target, pod, debugID, debugContainer, targetContainer, createdAt)...)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	cmd = execwrap.Command("kubectl", labelDebugPodArgs(target, pod)...)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// markDebugPodArgs builds kubectl annotate args for Podplane debug pods.
func markDebugPodArgs(target AppTargetOptions, pod string, debugID string, debugContainer string, targetContainer string, createdAt time.Time) []string {
	metadata := fmt.Sprintf(`{"container":"%s","target":"%s","createdAt":"%s"}`, debugContainer, targetContainer, createdAt.Format(time.RFC3339))
	args := kubectlContextArgs(target)
	args = append(args,
		"annotate", "pod", pod,
		"podplane.dev/shell-debug-"+debugID+"="+metadata,
		"--overwrite",
	)
	return args
}

// labelDebugPodArgs builds kubectl label args for Podplane debug pods.
func labelDebugPodArgs(target AppTargetOptions, pod string) []string {
	args := kubectlContextArgs(target)
	args = append(args,
		"label", "pod", pod,
		"podplane.dev/shell-debug=true",
		"--overwrite",
	)
	return args
}

// runKubectlExec runs one kubectl exec command with inherited streams.
func runKubectlExec(target AppTargetOptions, pod string, container string, tty bool, command []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	cmd := execwrap.Command("kubectl", shellArgs(target, pod, container, tty, command)...)
	cmd.Stdin = readerOrDefault(stdin, os.Stdin)
	cmd.Stdout = writerOrDefault(stdout, os.Stdout)
	cmd.Stderr = writerOrDefault(stderr, os.Stderr)
	return cmd.Run()
}

// shellPromptEnvSupported reports whether prompt env state can be applied remotely.
func shellPromptEnvSupported(target AppTargetOptions, pod string, container string) bool {
	return runKubectlExec(target, pod, container, false, []string{"env"}, strings.NewReader(""), io.Discard, io.Discard) == nil
}

// runShellPrompt starts the small fallback prompt for shell-less containers.
func runShellPrompt(opts ShellOptions, target AppTargetOptions, pod string, container string, envSupported bool) error {
	in := readerOrDefault(opts.Stdin, os.Stdin)
	out := writerOrDefault(opts.Stdout, os.Stdout)
	errOut := writerOrDefault(opts.Stderr, os.Stderr)
	prompt := shellPrompt{
		prompt:       fmt.Sprintf("%s:%s$ ", opts.Name, container),
		in:           in,
		out:          out,
		err:          errOut,
		envSupported: envSupported,
		run: func(env map[string]string, argv []string) error {
			return runKubectlExecQuiet(target, pod, container, shellPromptCommand(env, argv), in, out)
		},
	}
	return prompt.runPrompt()
}

// runKubectlExecQuiet runs one kubectl exec command and captures stderr for trimming.
func runKubectlExecQuiet(target AppTargetOptions, pod string, container string, command []string, stdin io.Reader, stdout io.Writer) error {
	var stderr bytes.Buffer
	cmd := execwrap.Command("kubectl", shellArgs(target, pod, container, false, command)...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s", shellPromptExecError(command, stderr.String(), err))
	}
	return nil
}

// readerOrDefault returns fallback when value is nil.
func readerOrDefault(value io.Reader, fallback io.Reader) io.Reader {
	if value == nil {
		return fallback
	}
	return value
}

// writerOrDefault returns fallback when value is nil.
func writerOrDefault(value io.Writer, fallback io.Writer) io.Writer {
	if value == nil {
		return fallback
	}
	return value
}
