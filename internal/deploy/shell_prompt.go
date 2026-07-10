// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

// shellPrompt is a small prompt used when a container has no shell.
type shellPrompt struct {
	prompt       string
	in           io.Reader
	out          io.Writer
	err          io.Writer
	env          map[string]string
	envSupported bool
	run          func(env map[string]string, argv []string) error
}

// runPrompt reads commands and runs each one as a separate exec call.
func (p *shellPrompt) runPrompt() error {
	if p.env == nil {
		p.env = map[string]string{}
	}
	if p.prompt == "" {
		p.prompt = "$ "
	}

	_, _ = fmt.Fprintln(p.out, "Podplane shell prompt: no bash/sh found; each command runs as a new process.")
	if p.envSupported {
		_, _ = fmt.Fprintln(p.out, "Virtual commands: export, unset, env, exit.")
	} else {
		_, _ = fmt.Fprintln(p.out, "Virtual commands: exit.")
	}
	scanner := bufio.NewScanner(p.in)
	for {
		_, _ = fmt.Fprint(p.out, p.prompt)
		if !scanner.Scan() {
			break
		}
		argv, err := shellPromptSplit(strings.TrimSpace(scanner.Text()))
		if err != nil {
			_, _ = fmt.Fprintf(p.err, "%v\n", err)
			continue
		}
		if len(argv) == 0 {
			continue
		}
		handled, done, err := p.handleBuiltin(argv)
		if err != nil {
			_, _ = fmt.Fprintf(p.err, "%v\n", err)
			continue
		}
		if done {
			break
		}
		if handled {
			continue
		}
		if p.run == nil {
			return fmt.Errorf("shell prompt runner is nil")
		}
		if err := p.run(cloneShellPromptEnv(p.env), argv); err != nil {
			_, _ = fmt.Fprintf(p.err, "%v\n", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read shell prompt input: %w", err)
	}
	return nil
}

// shellPromptExecError returns a short error for failed prompt commands.
func shellPromptExecError(argv []string, stderr string, err error) string {
	if len(argv) == 0 {
		return err.Error()
	}
	if message := strings.TrimSpace(stderr); message != "" {
		execPrefix := fmt.Sprintf("exec: %q:", argv[0])
		escapedExecPrefix := fmt.Sprintf(`exec: \"%s\":`, argv[0])
		if containsShellPromptExecError(message, execPrefix, escapedExecPrefix, " executable file not found", " stat ") {
			return fmt.Sprintf("%s: command not found", argv[0])
		}
		if containsShellPromptExecError(message, execPrefix, escapedExecPrefix, " permission denied") {
			return fmt.Sprintf("%s: permission denied", argv[0])
		}
		return compactKubectlError(message)
	}
	return err.Error()
}

// containsShellPromptExecError reports whether message includes an exec detail.
func containsShellPromptExecError(message string, prefix string, escapedPrefix string, details ...string) bool {
	for _, detail := range details {
		if strings.Contains(message, prefix+detail) || strings.Contains(message, escapedPrefix+detail) {
			return true
		}
	}
	return false
}

// compactKubectlError removes repeated Kubernetes internal error prefixes.
func compactKubectlError(message string) string {
	for {
		trimmed := strings.TrimPrefix(message, "error: Internal error occurred: ")
		trimmed = strings.TrimPrefix(trimmed, "Internal error occurred: ")
		if trimmed == message {
			return message
		}
		message = trimmed
	}
}

// handleBuiltin handles prompt-local commands.
func (p *shellPrompt) handleBuiltin(argv []string) (bool, bool, error) {
	switch argv[0] {
	case "exit", "quit":
		return true, true, nil
	case "cd":
		return true, false, fmt.Errorf("cd is not available in the shell prompt because the container has no shell to change directories before running commands")
	case "pwd":
		return true, false, fmt.Errorf("pwd is not available in the shell prompt because Kubernetes exec does not expose the remote working directory")
	case "export":
		if !p.envSupported {
			return true, false, fmt.Errorf("export is not available because the container has no env binary")
		}
		return true, false, p.export(argv[1:])
	case "unset":
		if !p.envSupported {
			return true, false, fmt.Errorf("unset is not available because the container has no env binary")
		}
		for _, key := range argv[1:] {
			delete(p.env, key)
		}
		return true, false, nil
	case "env":
		if len(argv) == 1 {
			if !p.envSupported {
				return true, false, fmt.Errorf("env is not available because the container has no env binary")
			}
			printShellPromptEnv(p.out, p.env)
			return true, false, nil
		}
	}
	return false, false, nil
}

// export records environment assignments for future prompt commands.
func (p *shellPrompt) export(assignments []string) error {
	if len(assignments) == 0 {
		printShellPromptEnv(p.out, p.env)
		return nil
	}
	for _, assignment := range assignments {
		key, value, ok := strings.Cut(assignment, "=")
		if !ok || key == "" {
			return fmt.Errorf("usage: export KEY=value")
		}
		p.env[key] = value
	}
	return nil
}

// shellPromptCommand adds prompt environment to a remote command.
func shellPromptCommand(env map[string]string, argv []string) []string {
	if len(env) == 0 {
		return argv
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	command := []string{"env"}
	for _, key := range keys {
		command = append(command, key+"="+env[key])
	}
	return append(command, argv...)
}

// shellPromptSplit tokenizes a command line using simple shell-like quotes.
func shellPromptSplit(line string) ([]string, error) {
	fields := []string{}
	var b strings.Builder
	quote := rune(0)
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t':
			if b.Len() > 0 {
				fields = append(fields, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		b.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	if b.Len() > 0 {
		fields = append(fields, b.String())
	}
	return fields, nil
}

// printShellPromptEnv prints environment assignments in stable order.
func printShellPromptEnv(out io.Writer, env map[string]string) {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		_, _ = fmt.Fprintf(out, "%s=%s\n", key, env[key])
	}
}

// cloneShellPromptEnv returns a copy of env.
func cloneShellPromptEnv(env map[string]string) map[string]string {
	clone := make(map[string]string, len(env))
	for key, value := range env {
		clone[key] = value
	}
	return clone
}
