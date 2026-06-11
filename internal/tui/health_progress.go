// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import "github.com/podplane/podplane/internal/health"

// RunHealthProgress renders health checks with the generic status progress UI.
func RunHealthProgress(title string, checks []health.Check, poll func() (map[string]health.Result, error)) error {
	items := healthStatusProgressItems(checks)
	required := requiredHealthStatusProgressItems(checks)
	return RunStatusProgress(title, items, required, func() (map[string]StatusProgressStatus, error) {
		results, err := poll()
		if err != nil {
			return nil, err
		}
		return healthStatusProgressStatuses(results), nil
	})
}

// healthStatusProgressItems converts reusable health checks into TUI rows.
func healthStatusProgressItems(checks []health.Check) []StatusProgressItem {
	items := make([]StatusProgressItem, 0, len(checks))
	for _, check := range checks {
		items = append(items, StatusProgressItem{Key: check.Key, Name: check.Name, Kind: check.Kind})
	}
	return items
}

// requiredHealthStatusProgressItems returns the TUI rows that must become ready
// before the progress display can exit.
func requiredHealthStatusProgressItems(checks []health.Check) []StatusProgressItem {
	items := []StatusProgressItem{}
	for _, check := range checks {
		if check.Required {
			items = append(items, StatusProgressItem{Key: check.Key, Name: check.Name, Kind: check.Kind})
		}
	}
	return items
}

// healthStatusProgressStatuses converts health check results into TUI status
// snapshots.
func healthStatusProgressStatuses(results map[string]health.Result) map[string]StatusProgressStatus {
	out := make(map[string]StatusProgressStatus, len(results))
	for key, result := range results {
		out[key] = StatusProgressStatus{
			Exists:  result.Exists,
			Ready:   result.Ready,
			Status:  string(result.Status),
			Message: result.Message,
		}
	}
	return out
}
