// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package kubectl

import "fmt"

// Error is returned when a kubectl invocation fails.
type Error struct {
	Stage  string
	Err    error
	Stderr string
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("kubectl %s: %v", e.Stage, e.Err)
	}
	return fmt.Sprintf("kubectl %s: %v: %s", e.Stage, e.Err, e.Stderr)
}

// Unwrap returns the underlying exec error.
func (e *Error) Unwrap() error {
	return e.Err
}
