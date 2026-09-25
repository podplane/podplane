// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/podplane/podplane/internal/health"
)

// TestHealthCriticalPathExpected uses the longest dependency path rather than
// summing every health check expectation.
func TestHealthCriticalPathExpected(t *testing.T) {
	checks := []health.Check{
		{Key: "cilium", Required: true, Expected: 30 * time.Second},
		{Key: "cert-manager", Required: true, DependsOn: []string{"cilium"}, Expected: 30 * time.Second},
		{Key: "cert-manager-admission", Required: true, DependsOn: []string{"cert-manager"}, Expected: 10 * time.Second},
		{Key: "envoy-gateway", Required: true, DependsOn: []string{"cilium"}, Expected: 20 * time.Second},
		{Key: "ingress", Required: true, DependsOn: []string{"envoy-gateway"}, Expected: 5 * time.Second},
	}

	if got, want := healthCriticalPathExpected(checks), 70*time.Second; got != want {
		t.Fatalf("healthCriticalPathExpected() = %s, want %s", got, want)
	}
}

// TestRunHealthTaskProgressSuppressesPendingMessages keeps expected transient
// startup failures from dominating the local-start dashboard while preserving
// the success message when the check becomes ready.
func TestRunHealthTaskProgressSuppressesPendingMessages(t *testing.T) {
	attempts := 0
	checks := []health.Check{
		{
			Key:      "webhook",
			Name:     "cert-manager admission",
			Required: true,
			Run: func(context.Context) health.Result {
				attempts++
				if attempts == 1 {
					return health.Result{Exists: true, Status: health.StatusPending, Message: "Error from server: failed calling webhook"}
				}
				return health.Result{Exists: true, Ready: true, Status: health.StatusReady, Message: "ready"}
			},
		},
	}
	events := []TaskProgressEvent{}
	progress := TaskProgress(func(event TaskProgressEvent) {
		events = append(events, event)
	})

	if _, err := RunHealthTaskProgress(context.Background(), checks, progress); err != nil {
		t.Fatalf("RunHealthTaskProgress returned error: %v", err)
	}

	for _, event := range events {
		if event.Type == TaskProgressInfo && strings.Contains(event.Message, "failed calling webhook") {
			t.Fatalf("RunHealthTaskProgress emitted pending failure detail: %#v", event)
		}
	}
	last := events[len(events)-1]
	if last.Type != TaskProgressDone || last.Message != "ready" {
		t.Fatalf("last event = %#v, want done ready", last)
	}
}

// TestHealthProgressPollerTimeoutIncludesLastPendingMessage ensures suppressed
// pending details remain available when a health check actually times out.
func TestHealthProgressPollerTimeoutIncludesLastPendingMessage(t *testing.T) {
	checks := []health.Check{
		{
			Key:      "ingress",
			Name:     "local ingress proxy",
			Required: true,
			Timeout:  time.Nanosecond,
			Run: func(context.Context) health.Result {
				return health.Result{Exists: true, Status: health.StatusPending, Message: "waiting for envoy-gateway: HTTP 502"}
			},
		},
	}
	poller := newHealthProgressPoller(context.Background(), checks, false)
	if _, err := poller.poll(); err != nil {
		t.Fatalf("first poll returned error: %v", err)
	}
	time.Sleep(time.Millisecond)

	_, err := poller.poll()
	if err == nil {
		t.Fatal("second poll succeeded, want timeout")
	}
	if !strings.Contains(err.Error(), "waiting for envoy-gateway: HTTP 502") {
		t.Fatalf("timeout error = %q, want last pending message", err)
	}
}
