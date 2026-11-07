// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import "testing"

// TestSplitConfirmMessageSplitsTitleAndBody verifies newline-based layout hints.
func TestSplitConfirmMessageSplitsTitleAndBody(t *testing.T) {
	t.Parallel()
	title, body := splitConfirmMessage("Title\nBody text")
	if title != "Title" || body != "Body text" {
		t.Fatalf("splitConfirmMessage = %q, %q", title, body)
	}
}

// TestWrapWordsWrapsLongText verifies small terminal wrapping.
func TestWrapWordsWrapsLongText(t *testing.T) {
	t.Parallel()
	got := wrapWords("Restart deployment hello now", 14)
	want := "Restart\ndeployment\nhello now"
	if got != want {
		t.Fatalf("wrapWords = %q, want %q", got, want)
	}
	styled := "\x1b[1mBucket Name: example-cluster\x1b[0m\n\nPrivate and encrypted"
	if got := wrapWords(styled, 28); got != styled {
		t.Fatalf("wrapping lost styling or paragraph breaks: %q", got)
	}
}
