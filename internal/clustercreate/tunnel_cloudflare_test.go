// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clustercreate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbletea"
)

const testTunnelID = "6d27d327-213f-4de7-894b-f97e24e13208"
const testTunnelSecret = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
const testTunnelJSON = `{
  "AccountTag": "0123456789abcdef0123456789abcdef",
  "TunnelSecret": "` + testTunnelSecret + `",
  "TunnelID": "` + testTunnelID + `"
}`

// memoryTunnelStore simulates secret storage, permission failures and concurrent creation.
type memoryTunnelStore struct {
	value                         []byte
	exists                        bool
	existsErr, readErr, createErr error
	raceValue                     []byte
}

// Exists returns the simulated metadata result without reading credentials.
func (s *memoryTunnelStore) Exists(context.Context) (bool, error) { return s.exists, s.existsErr }

// Read returns a copy so clearing the result does not erase the stored secret.
func (s *memoryTunnelStore) Read(context.Context) ([]byte, error) {
	return append([]byte(nil), s.value...), s.readErr
}

// Name returns the test secret's path.
func (s *memoryTunnelStore) Name() string { return "/example-cluster/tunnel-credentials" }

// Close is a no-op for in-memory storage.
func (s *memoryTunnelStore) Close() error { return nil }

// Create refuses replacement, including when another caller creates the secret first.
func (s *memoryTunnelStore) Create(_ context.Context, value []byte) error {
	if s.raceValue != nil {
		s.exists, s.value = true, s.raceValue
		return errTunnelSecretExists
	}
	if s.exists {
		return errTunnelSecretExists
	}
	if s.createErr != nil {
		return s.createErr
	}
	s.exists, s.value = true, append([]byte(nil), value...)
	return nil
}

// testCloudflareForm completes the initial lookup and arranges cancellation on cleanup.
func testCloudflareForm(t *testing.T, store *memoryTunnelStore) cloudflareForm {
	t.Helper()
	m := newCloudflareForm(context.Background(), func(context.Context) (tunnelStore, error) { return store, nil })
	t.Cleanup(m.cancel)
	model, _ := m.Update(m.Init()())
	return model.(cloudflareForm)
}

// TestCloudflareSecretLookup distinguishes missing secrets from permission failures
// and refuses to conceal readable malformed credentials behind a manual UUID.
func TestCloudflareSecretLookup(t *testing.T) {
	for _, tt := range []struct {
		name  string
		store memoryTunnelStore
		state string
		done  bool
	}{
		{"missing", memoryTunnelStore{}, "review", false},
		{"metadata denied", memoryTunnelStore{existsErr: errors.New("denied")}, "manual", false},
		{"read denied", memoryTunnelStore{exists: true, readErr: errors.New("denied")}, "manual", false},
		{"malformed", memoryTunnelStore{exists: true, value: []byte(`{"TunnelSecret":"sensitive"}`)}, "invalid", false},
		{"existing", memoryTunnelStore{exists: true, value: []byte(testTunnelJSON)}, "lookup", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := testCloudflareForm(t, &tt.store)
			if m.state != tt.state || m.done != tt.done {
				t.Fatalf("state=%s done=%v", m.state, m.done)
			}
			if m.done && m.id != testTunnelID {
				t.Fatalf("UUID = %s", m.id)
			}
			if strings.Contains(m.View(), "sensitive") || strings.Contains(m.View(), testTunnelSecret) {
				t.Fatal("secret leaked")
			}
		})
	}
}

// TestCloudflarePasteReviewAndSave exercises editing, masking, validation and
// explicit creation, including a concurrent creator with a different UUID.
func TestCloudflarePasteReviewAndSave(t *testing.T) {
	for _, race := range []bool{false, true} {
		store := &memoryTunnelStore{}
		m := testCloudflareForm(t, store)
		press := func(msg tea.KeyMsg) tea.Cmd {
			model, cmd := m.Update(msg)
			m = model.(cloudflareForm)
			return cmd
		}
		press(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune(testTunnelJSON)})
		if store.exists || !strings.Contains(m.View(), testTunnelID) || strings.Contains(m.View(), testTunnelSecret) {
			t.Fatal("paste must only show a masked review")
		}
		press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
		if !strings.Contains(m.View(), "Secret visible while editing") || !strings.Contains(m.View(), testTunnelSecret) {
			t.Fatal("edit must reveal credentials with a warning")
		}
		press(tea.KeyMsg{Type: tea.KeyEsc})
		if strings.Contains(m.View(), testTunnelSecret) {
			t.Fatal("review leaked secret after edit")
		}
		press(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune(`{"TunnelSecret":"sensitive"}`)})
		if cmd := press(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil || store.exists {
			t.Fatal("invalid credentials should not be saved")
		}
		press(tea.KeyMsg{Type: tea.KeyRunes, Paste: true})
		if !strings.Contains(m.View(), "<Cloudflare account ID>") {
			t.Fatal("empty input should restore placeholder")
		}
		press(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune(testTunnelJSON)})
		want := testTunnelID
		if race {
			want = "038fb42b-d2ef-49c2-a435-4ad3810b84fb"
			store.raceValue = []byte(strings.ReplaceAll(testTunnelJSON, testTunnelID, want))
		}
		cmd := press(tea.KeyMsg{Type: tea.KeyEnter})
		model, _ := m.Update(cmd())
		m = model.(cloudflareForm)
		if !m.done || m.id != want || !strings.Contains(string(store.value), want) {
			t.Fatalf("saved UUID = %s, want %s", m.id, want)
		}
	}
}

// TestCloudflareSkipLookup ignores late lookup results and uses the manually
// supplied UUID without creating a secret.
func TestCloudflareSkipLookup(t *testing.T) {
	store := &memoryTunnelStore{}
	m := newCloudflareForm(context.Background(), func(context.Context) (tunnelStore, error) { return store, nil })
	t.Cleanup(m.cancel)
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(cloudflareForm)
	model, _ = m.Update(cloudflareResult{lookup: cloudflareLookup{missing: true}})
	m = model.(cloudflareForm)
	if m.state != "manual" {
		t.Fatal("late result replaced manual input")
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Paste: true, Runes: []rune(testTunnelID)})
	m = model.(cloudflareForm)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(cloudflareForm)
	if !m.done || m.id != testTunnelID || store.exists {
		t.Fatal("manual UUID did not complete without a write")
	}
}
