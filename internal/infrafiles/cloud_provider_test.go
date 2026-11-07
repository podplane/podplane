// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package infrafiles

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbletea"

	"github.com/podplane/podplane/internal/tui"
)

// TestCloudProviderItemsDefaultToAWSAndIncludeCancel checks provider ordering and cancellation.
func TestCloudProviderItemsDefaultToAWSAndIncludeCancel(t *testing.T) {
	items := cloudProviderItems()
	if len(items) != 3 {
		t.Fatalf("cloud provider item count = %d, want 3", len(items))
	}

	aws, ok := items[0].(tui.Item)
	if !ok {
		t.Fatalf("first cloud provider item has type %T, want tui.Item", items[0])
	}
	if aws.Key != "aws" || aws.Label != "Amazon Web Services (AWS)" || aws.Cancel {
		t.Fatalf("first cloud provider item = %#v, want default AWS option", aws)
	}

	google := items[1].(tui.Item)
	if google.Key != "google" || google.Label != "Google Cloud" || google.Cancel {
		t.Fatalf("second cloud provider item = %#v, want Google Cloud option", google)
	}

	cancel := items[2].(tui.Item)
	if cancel.Label != "Cancel" || !cancel.Cancel {
		t.Fatalf("third cloud provider item = %#v, want cancel option", cancel)
	}
}

// TestAWSLookupCancellation checks that manual entry and abort both stop a
// pending lookup, while remaining distinct outcomes for the caller.
func TestAWSLookupCancellation(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		ctx, stop := context.WithTimeout(t.Context(), 3*time.Second)
		lookupCtx, cancel := context.WithCancel(ctx)
		started, finished := make(chan struct{}), make(chan struct{})
		program := tea.NewProgram(awsAccountLookup{
			cancel: cancel, spinner: spinner.New(), width: 80,
			lookup: func() (string, error) {
				close(started)
				<-lookupCtx.Done()
				close(finished)
				return "", lookupCtx.Err()
			},
		}, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer())
		go func() {
			select {
			case <-started:
				program.Send(tea.KeyMsg{Type: key})
				if key == tea.KeyEsc {
					program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("987654")})
					program.Send(tea.KeyMsg{Type: tea.KeyEsc})
					program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("321098")})
					program.Send(tea.KeyMsg{Type: tea.KeyEnter})
				}
			case <-ctx.Done():
			}
		}()
		result, err := program.Run()
		if err != nil {
			cancel()
			stop()
			t.Fatal(err)
		}
		got := result.(awsAccountLookup)
		wantAccount := ""
		if key == tea.KeyEsc {
			wantAccount = "987654321098"
		}
		if got.manual != (key == tea.KeyEsc) || got.aborted != (key == tea.KeyCtrlC) || got.account != wantAccount {
			t.Fatalf("lookup outcome for %v: %+v", key, got)
		}
		select {
		case <-finished:
		case <-ctx.Done():
			t.Error("lookup did not stop after cancellation")
		}
		cancel()
		stop()
	}
}

// TestEscapeAfterAWSLookupPreservesInput covers lookup completion racing with
// Escape and ensures stale results cannot overwrite a manually entered account.
func TestEscapeAfterAWSLookupPreservesInput(t *testing.T) {
	for _, result := range []awsAccountResult{
		{account: "123456789012"},
		{err: context.DeadlineExceeded},
	} {
		form := awsAccountLookup{cancel: func() {}, width: 80}
		model, _ := form.Update(result)
		form = model.(awsAccountLookup)
		if form.done {
			t.Fatal("lookup quit before confirmation")
		}
		form.input.SetValue("987654321098")
		model, _ = form.Update(tea.KeyMsg{Type: tea.KeyEsc})
		form = model.(awsAccountLookup)
		model, _ = form.Update(awsAccountResult{account: "111111111111"})
		form = model.(awsAccountLookup)
		if form.done || form.input.Value() != "987654321098" {
			t.Fatal("Escape or stale lookup result discarded the manual answer")
		}
		model, _ = form.Update(tea.KeyMsg{Type: tea.KeyEnter})
		form = model.(awsAccountLookup)
		if !form.done || form.aborted || form.account != "987654321098" {
			t.Fatalf("confirmation failed: %+v", form)
		}
	}
}
