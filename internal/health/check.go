// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"context"
	"fmt"
)

// Status is the current state of a health check.
type Status string

const (
	// StatusPending means the check target is not ready yet.
	StatusPending Status = "Pending"
	// StatusReady means the check target is healthy.
	StatusReady Status = "Ready"
	// StatusFailed means the check target is unhealthy.
	StatusFailed Status = "Failed"
)

// Result is one health check observation.
type Result struct {
	Exists  bool
	Ready   bool
	Status  Status
	Message string
	Err     error
}

// Check is a reusable cluster health check.
type Check struct {
	Key      string
	Name     string
	Kind     string
	Required bool
	Run      func(context.Context) Result
}

// RunChecks observes all checks once.
func RunChecks(ctx context.Context, checks []Check) (map[string]Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results := make(map[string]Result, len(checks))
	for _, check := range checks {
		if check.Run == nil {
			return nil, fmt.Errorf("health check %q has no run function", check.Key)
		}
		result := check.Run(ctx)
		if result.Err != nil {
			return nil, result.Err
		}
		if result.Status == "" {
			if result.Ready {
				result.Status = StatusReady
			} else {
				result.Status = StatusPending
			}
		}
		results[check.Key] = result
	}
	return results, nil
}

// key returns the stable identifier used to join checks and results.
func key(namespace, kind, name string) string {
	return namespace + "/" + kind + "/" + name
}
