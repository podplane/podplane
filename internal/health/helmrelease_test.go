// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package health

import "testing"

// TestHelmReleaseReadyStatus verifies Flux Ready condition normalization.
func TestHelmReleaseReadyStatus(t *testing.T) {
	ready, status, message := HelmReleaseReadyStatus([]HelmReleaseCondition{{Type: "Ready", Status: "True"}})
	if !ready || status != "Ready" || message == "" {
		t.Fatalf("ready status = (%v, %q, %q), want ready with message", ready, status, message)
	}

	ready, status, message = HelmReleaseReadyStatus([]HelmReleaseCondition{{Type: "Ready", Status: "False", Reason: "Progressing", Message: "installing"}})
	if ready || status != "Progressing" || message != "installing" {
		t.Fatalf("not-ready status = (%v, %q, %q), want false Progressing installing", ready, status, message)
	}

	ready, status, message = HelmReleaseReadyStatus(nil)
	if ready || status != "Reconciling" || message == "" {
		t.Fatalf("missing status = (%v, %q, %q), want false Reconciling with message", ready, status, message)
	}
}
