// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/bubbletea"
)

// TestVerificationSkipAndCancel runs the real event loop with a pending request.
func TestVerificationSkipAndCancel(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		started, stopped := make(chan struct{}), make(chan struct{})
		m := verificationProgram{NewVerification(ctx, "Checking cloud access", "Bucket", func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(stopped)
			return ctx.Err()
		})}
		p := tea.NewProgram(&m, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer())
		go func() {
			select {
			case <-started:
				p.Send(tea.KeyMsg{Type: key})
			case <-ctx.Done():
			}
		}()
		_, err := p.Run()
		if err != nil {
			t.Fatal(err)
		}
		if !m.Done || m.Skipped != (key == tea.KeyEsc) || errors.Is(m.Err, context.Canceled) != (key == tea.KeyCtrlC) {
			t.Fatalf("incorrect skip/cancel outcome: %+v", m.Verification)
		}
		select {
		case <-stopped:
		case <-ctx.Done():
			t.Fatal("request did not stop")
		}
		cancel()
	}
}

// TestVerificationRetryAndLateResults checks failed attempts can be retried and
// a cancelled check's late result cannot complete another check.
func TestVerificationRetryAndLateResults(t *testing.T) {
	failure := errors.New("access denied")
	m := NewVerification(t.Context(), "Checking", "Zone", func(context.Context) error { return nil })
	m.Start()
	old := m.attempt
	m.Cancel()
	m.Update(verificationResult{old, failure})
	if m.Done {
		t.Fatal("failure should offer retry or skip")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	defer m.Cancel()
	m.Update(verificationResult{old, nil})
	if m.Done {
		t.Fatal("late result completed a new attempt")
	}
	m.Update(verificationResult{m.attempt, nil})
	if !m.Done || m.Err != nil || m.Skipped {
		t.Fatal("successful retry did not complete")
	}
}
