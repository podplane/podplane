// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/podplane/podplane/internal/health"
)

// HealthProgressOptions configures health check progress rendering.
type HealthProgressOptions struct {
	Context        context.Context
	Title          string
	ShowTiming     bool
	SuccessMessage string
}

// RunHealthProgress renders dependency-aware health checks with the generic
// status progress UI.
func RunHealthProgress(opts HealthProgressOptions, checks []health.Check) error {
	if len(checks) == 0 {
		return nil
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	title := opts.Title
	if title == "" {
		title = "Checking health"
	}
	if opts.ShowTiming {
		if expected := healthCriticalPathExpected(checks); expected > 0 {
			title = fmt.Sprintf("%s · expected ~%s", title, formatDuration(expected))
		}
	}
	poller := newHealthProgressPoller(ctx, checks, opts.ShowTiming)
	if err := RunStatusProgress(title, healthStatusProgressItems(checks), requiredHealthStatusProgressItems(checks), poller.poll); err != nil {
		return err
	}
	if opts.ShowTiming && opts.SuccessMessage != "" {
		_, _ = fmt.Fprintf(os.Stdout, "✓ %s in %s\n", opts.SuccessMessage, formatDuration(poller.elapsed()))
	}
	return nil
}

// RunHealthTaskProgress reports health check progress through TaskProgress so
// callers can include checks in a larger upfront progress plan.
func RunHealthTaskProgress(ctx context.Context, checks []health.Check, progress TaskProgress) (time.Duration, error) {
	if len(checks) == 0 {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	poller := newHealthProgressPoller(ctx, checks, false)
	started := map[string]bool{}
	done := map[string]bool{}
	for {
		statuses, err := poller.poll()
		if err != nil {
			return poller.elapsed(), err
		}
		for _, check := range checks {
			status := statuses[check.Key]
			if status.Status == string(health.Status("Blocked")) {
				continue
			}
			if !started[check.Key] {
				started[check.Key] = true
				progress.Started(check.Key, check.Name, "")
			}
			if status.Ready && !done[check.Key] {
				done[check.Key] = true
				progress.Done(check.Key, check.Name, status.Message)
				continue
			}
			if !status.Ready && status.Message != "" && status.Status != string(health.StatusPending) {
				progress(TaskProgressEvent{Type: TaskProgressInfo, Key: check.Key, Name: check.Name, Message: status.Message})
			}
		}
		if statusProgressRequiredReady(statuses, requiredHealthStatusProgressItems(checks)) {
			return poller.elapsed(), nil
		}
		select {
		case <-ctx.Done():
			return poller.elapsed(), ctx.Err()
		case <-time.After(statusProgressPollInterval):
		}
	}
}

type healthProgressPoller struct {
	ctx       context.Context
	checks    []health.Check
	checkByID map[string]health.Check
	showTime  bool
	startedAt map[string]time.Time
	doneAt    map[string]time.Time
	statuses  map[string]health.Result
	firstPoll time.Time
}

// newHealthProgressPoller creates the stateful poller used by RunHealthProgress.
func newHealthProgressPoller(ctx context.Context, checks []health.Check, showTime bool) *healthProgressPoller {
	checkByID := make(map[string]health.Check, len(checks))
	for _, check := range checks {
		checkByID[check.Key] = check
	}
	return &healthProgressPoller{
		ctx:       ctx,
		checks:    checks,
		checkByID: checkByID,
		showTime:  showTime,
		startedAt: map[string]time.Time{},
		doneAt:    map[string]time.Time{},
		statuses:  map[string]health.Result{},
	}
}

// poll observes every unblocked health check once.
func (p *healthProgressPoller) poll() (map[string]StatusProgressStatus, error) {
	if p.firstPoll.IsZero() {
		p.firstPoll = time.Now()
	}
	now := time.Now()
	if err := p.ctx.Err(); err != nil {
		return nil, err
	}
	for _, check := range p.checks {
		if p.statuses[check.Key].Ready {
			continue
		}
		if dep := p.blockingDependency(check); dep != "" {
			p.statuses[check.Key] = health.Result{Status: health.Status("Blocked"), Message: "waiting for " + dep}
			continue
		}
		if p.startedAt[check.Key].IsZero() {
			p.startedAt[check.Key] = now
		}
		if check.Timeout > 0 && now.Sub(p.startedAt[check.Key]) > check.Timeout {
			message := p.statuses[check.Key].Message
			if message != "" {
				return nil, fmt.Errorf("%s timed out after %s: %s", check.Name, formatDuration(check.Timeout), message)
			}
			return nil, fmt.Errorf("%s timed out after %s", check.Name, formatDuration(check.Timeout))
		}
		if check.Run == nil {
			return nil, fmt.Errorf("health check %q has no run function", check.Key)
		}
		result := check.Run(p.ctx)
		if result.Err != nil {
			return nil, result.Err
		}
		if result.Status == "" {
			if result.Ready {
				result.Status = health.StatusReady
			} else {
				result.Status = health.StatusPending
			}
		}
		if result.Ready {
			p.doneAt[check.Key] = now
		}
		p.statuses[check.Key] = result
	}
	return healthStatusProgressStatuses(p.checks, p.statuses, p.startedAt, p.doneAt, p.showTime), nil
}

// blockingDependency returns the display name of the first unresolved dependency.
func (p *healthProgressPoller) blockingDependency(check health.Check) string {
	for _, dep := range check.DependsOn {
		if !p.statuses[dep].Ready {
			if depCheck, ok := p.checkByID[dep]; ok && depCheck.Name != "" {
				return depCheck.Name
			}
			return dep
		}
	}
	return ""
}

// elapsed returns wall-clock duration since health checks first polled.
func (p *healthProgressPoller) elapsed() time.Duration {
	if p.firstPoll.IsZero() {
		return 0
	}
	return time.Since(p.firstPoll)
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
func healthStatusProgressStatuses(checks []health.Check, results map[string]health.Result, startedAt, doneAt map[string]time.Time, showTime bool) map[string]StatusProgressStatus {
	out := make(map[string]StatusProgressStatus, len(results))
	for _, check := range checks {
		result := results[check.Key]
		status := string(result.Status)
		if showTime {
			status = healthTimedStatus(check, result, startedAt[check.Key], doneAt[check.Key])
		}
		out[check.Key] = StatusProgressStatus{
			Exists:  result.Exists,
			Ready:   result.Ready,
			Status:  status,
			Message: result.Message,
		}
	}
	return out
}

// healthTimedStatus renders elapsed and expected timing for one health row.
func healthTimedStatus(check health.Check, result health.Result, startedAt, doneAt time.Time) string {
	if result.Status == health.Status("Blocked") || startedAt.IsZero() {
		return string(result.Status)
	}
	if result.Ready {
		if doneAt.IsZero() {
			return string(result.Status)
		}
		return formatDuration(doneAt.Sub(startedAt))
	}
	elapsed := time.Since(startedAt)
	if check.Expected <= 0 {
		return formatDuration(elapsed)
	}
	return fmt.Sprintf("%s/~%s", formatDuration(elapsed), formatDuration(check.Expected))
}

// healthCriticalPathExpected returns the longest dependency-path expected time.
func healthCriticalPathExpected(checks []health.Check) time.Duration {
	byID := make(map[string]health.Check, len(checks))
	for _, check := range checks {
		byID[check.Key] = check
	}
	memo := map[string]time.Duration{}
	var path func(string) time.Duration
	path = func(key string) time.Duration {
		if v, ok := memo[key]; ok {
			return v
		}
		check, ok := byID[key]
		if !ok {
			return 0
		}
		var deps time.Duration
		for _, dep := range check.DependsOn {
			if d := path(dep); d > deps {
				deps = d
			}
		}
		memo[key] = deps + check.Expected
		return memo[key]
	}
	var total time.Duration
	for _, check := range checks {
		if !check.Required {
			continue
		}
		if d := path(check.Key); d > total {
			total = d
		}
	}
	return total
}
