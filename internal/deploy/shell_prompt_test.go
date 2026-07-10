// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestShellPromptSplitHandlesQuotes verifies simple quoted argument splitting.
func TestShellPromptSplitHandlesQuotes(t *testing.T) {
	t.Parallel()
	got, err := shellPromptSplit(`echo "hello world" 'again'`)
	if err != nil {
		t.Fatalf("shellPromptSplit error = %v", err)
	}
	want := []string{"echo", "hello world", "again"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shellPromptSplit = %#v, want %#v", got, want)
	}
}

// TestShellPromptPersistsExportForCommands verifies prompt-local env state.
func TestShellPromptPersistsExportForCommands(t *testing.T) {
	t.Parallel()
	var calls []promptCall
	var out strings.Builder
	prompt := shellPrompt{
		prompt:       "app$ ",
		in:           strings.NewReader("export FOO=bar BAZ='two words'\nprintenv FOO\nunset FOO\nprintenv BAZ\nexit\n"),
		out:          &out,
		err:          &out,
		envSupported: true,
		run: func(env map[string]string, argv []string) error {
			calls = append(calls, promptCall{env: env, argv: append([]string(nil), argv...)})
			return nil
		},
	}
	if err := prompt.runPrompt(); err != nil {
		t.Fatalf("runPrompt error = %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if got, want := calls[0].env["FOO"], "bar"; got != want {
		t.Fatalf("FOO = %q, want %q", got, want)
	}
	if got, want := calls[0].env["BAZ"], "two words"; got != want {
		t.Fatalf("BAZ = %q, want %q", got, want)
	}
	if _, ok := calls[1].env["FOO"]; ok {
		t.Fatal("FOO still set after unset")
	}
}

// TestShellPromptRejectsExportWithoutEnv verifies env state depends on env support.
func TestShellPromptRejectsExportWithoutEnv(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	prompt := shellPrompt{in: strings.NewReader("export FOO=bar\nenv\nunset FOO\nexit\n"), out: &out, err: &out}
	if err := prompt.runPrompt(); err != nil {
		t.Fatalf("runPrompt error = %v", err)
	}
	text := out.String()
	for _, want := range []string{"export is not available", "env is not available", "unset is not available"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q: %q", want, text)
		}
	}
}

// TestShellPromptRejectsCD verifies the prompt does not fake cwd state.
func TestShellPromptRejectsCD(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	prompt := shellPrompt{in: strings.NewReader("cd /app\nexit\n"), out: &out, err: &out}
	if err := prompt.runPrompt(); err != nil {
		t.Fatalf("runPrompt error = %v", err)
	}
	if !strings.Contains(out.String(), "cd is not available") {
		t.Fatalf("output missing cd error: %q", out.String())
	}
}

// TestShellPromptErrorShortensMissingCommand verifies kubectl noise is hidden.
func TestShellPromptErrorShortensMissingCommand(t *testing.T) {
	t.Parallel()
	stderr := `error: Internal error occurred: Internal error occurred: error executing command in container: failed to exec in container: failed to start exec "abc": OCI runtime exec failed: exec failed: unable to start container process: exec: "ls": executable file not found in $PATH`
	if got, want := shellPromptExecError([]string{"ls"}, stderr, errShellPromptTest), "ls: command not found"; got != want {
		t.Fatalf("shellPromptExecError = %q, want %q", got, want)
	}
}

// promptCall records one prompt command invocation.
type promptCall struct {
	env  map[string]string
	argv []string
}

var errShellPromptTest = errors.New("exit status 1")
