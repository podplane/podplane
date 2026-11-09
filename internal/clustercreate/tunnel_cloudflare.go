// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clustercreate

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/tui"
)

const cloudflareCredentialPlaceholder = "{\n  \"AccountTag\": \"<Cloudflare account ID>\",\n  \"TunnelSecret\": \"<base64-encoded tunnel secret>\",\n  \"TunnelID\": \"<tunnel UUID>\"\n}"

const cloudflareTunnelDocsURL = "https://developers.cloudflare.com/tunnel/features/locally-managed-tunnels/"
const cloudflareTunnelDescription = "Using Cloudflare Tunnels requires a \"locally managed\" tunnel, with its credentials JSON stored in a cloud secret. This lets Podplane dynamically configure different routes for running nodes and the sleep/wake proxy when cluster sleep is enabled."

// cloudflareCredentials is the JSON credentials file for a locally managed tunnel.
type cloudflareCredentials struct {
	AccountTag   string
	TunnelSecret string
	TunnelID     string
}

// parseCloudflareCredentials deliberately returns errors without input fragments.
func parseCloudflareCredentials(raw string) (cloudflareCredentials, error) {
	var value cloudflareCredentials
	if len(raw) > 4096 || json.Unmarshal([]byte(raw), &value) != nil {
		return value, errors.New("paste a credentials JSON object (maximum 4096 bytes)")
	}
	account, err := hex.DecodeString(value.AccountTag)
	if err != nil || len(account) != 16 {
		return value, errors.New("AccountTag must be a 32-character hexadecimal account ID")
	}
	secret, err := base64.StdEncoding.DecodeString(value.TunnelSecret)
	defer clear(secret)
	if err != nil || len(secret) < 32 {
		return value, errors.New("TunnelSecret must contain at least 32 bytes encoded as base64")
	}
	if err := validateCloudflareTunnelID(value.TunnelID); err != nil {
		return value, err
	}
	return value, nil
}

// validateCloudflareTunnelID requires a nonzero UUID in its hyphenated form.
func validateCloudflareTunnelID(value string) error {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil || len(value) != 36 {
		return errors.New("TunnelID must be a non-empty tunnel UUID")
	}
	return nil
}

// cloudflareLookup distinguishes an absent secret from an unreadable or invalid one.
type cloudflareLookup struct {
	name, id, notice string
	missing, invalid bool
}

// lookupCloudflareTunnel reads metadata before credentials and returns safe UI diagnostics.
func lookupCloudflareTunnel(ctx context.Context, store tunnelStore) cloudflareLookup {
	result := cloudflareLookup{name: store.Name()}
	exists, err := store.Exists(ctx)
	if err != nil {
		result.notice = "Could not check the credentials secret with your current credentials. Enter the tunnel UUID manually; the secret must exist before deployment."
		return result
	}
	if !exists {
		result.missing = true
		return result
	}
	raw, err := store.Read(ctx)
	defer clear(raw)
	if err != nil {
		result.notice = "Could not read the tunnel ID from the credentials secret with your current credentials. Enter the tunnel UUID manually to continue."
		return result
	}
	credentials, err := parseCloudflareCredentials(string(raw))
	if err != nil {
		result.invalid, result.notice = true, "The existing credentials secret is invalid: "+err.Error()+". Correct it before deployment; entering a UUID cannot repair it."
		return result
	}
	result.id = credentials.TunnelID
	return result
}

// cloudflareResult delivers a lookup or creation outcome to the form.
type cloudflareResult struct {
	lookup cloudflareLookup
	err    error
	saving bool
}

// cloudflareForm manages secret lookup, masked review, editing and manual UUID entry.
type cloudflareForm struct {
	ctx                     context.Context
	open                    func(context.Context) (tunnelStore, error)
	cancel                  context.CancelFunc
	editor                  textarea.Model
	manual                  textinput.Model
	state, name, notice, id string
	err                     error
	width, height           int
	done                    bool
}

// configureCloudflareTunnel resolves the UUID without storing credentials in cluster config.
func configureCloudflareTunnel(ctx context.Context, cfg *clusterconfig.ClusterConfig) error {
	m := newCloudflareForm(ctx, func(ctx context.Context) (tunnelStore, error) { return newTunnelStore(ctx, cfg) })
	defer m.cancel()
	got, err := tui.Run(ctx, m)
	if err != nil {
		return err
	}
	form := got.(cloudflareForm)
	if form.err != nil {
		return form.err
	}
	if !form.done {
		return errors.New("tunnel configuration cancelled")
	}
	cfg.Cluster.Tunnel.ID = form.id
	return nil
}

// newCloudflareForm starts in lookup mode; the caller must cancel it on exit.
func newCloudflareForm(ctx context.Context, open func(context.Context) (tunnelStore, error)) cloudflareForm {
	ctx, cancel := context.WithCancel(ctx)
	editor := textarea.New()
	editor.Placeholder = cloudflareCredentialPlaceholder
	editor.CharLimit = 4096
	editor.SetWidth(76)
	editor.SetHeight(8)
	manual := textinput.New()
	manual.CharLimit = 36
	manual.Focus()
	return cloudflareForm{
		ctx: ctx, cancel: cancel, open: open,
		editor: editor, manual: manual,
		state: "lookup", width: 80, height: 24,
	}
}

// Init starts the credentials lookup without blocking the form.
func (m cloudflareForm) Init() tea.Cmd { return m.request(false) }

// request looks up or creates the secret with a deadline and closes its cloud client.
// Backend errors are replaced with safe diagnostics to avoid exposing credentials.
func (m cloudflareForm) request(save bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		store, err := m.open(ctx)
		if err != nil {
			return cloudflareResult{err: errors.New("could not access the credentials backend"), saving: save}
		}
		defer func() { _ = store.Close() }()
		if !save {
			return cloudflareResult{lookup: lookupCloudflareTunnel(ctx, store)}
		}
		raw := []byte(m.editor.Value())
		defer clear(raw)
		err = store.Create(ctx, raw)
		if errors.Is(err, errTunnelSecretExists) {
			return cloudflareResult{lookup: lookupCloudflareTunnel(ctx, store)}
		}
		if err != nil {
			return cloudflareResult{saving: true, err: errors.New("creation was not confirmed; the secret may already exist or be incomplete. Check it before retrying; existing secrets are never overwritten")}
		}
		credentials, _ := parseCloudflareCredentials(string(raw))
		return cloudflareResult{saving: true, lookup: cloudflareLookup{name: store.Name(), id: credentials.TunnelID}}
	}
}

// RunInline never echoes pasted secrets in terminals without the secure editor.
func (m cloudflareForm) RunInline(ctx context.Context) (tea.Model, error) {
	m.cancel()
	id, err := tui.Input(ctx, "Cloudflare Tunnels", "Tunnel UUID (ensure tunnel-credentials exists before deployment)", "", validateCloudflareTunnelID)
	m.id, m.done = id, err == nil
	return m, err
}

// Update handles lookup results and user input, ignoring results after a skipped lookup.
func (m cloudflareForm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.editor.SetWidth(max(10, m.width-4))
		m.editor.SetHeight(max(3, min(10, m.height-12)))
	case cloudflareResult:
		if m.state != "lookup" && m.state != "saving" {
			return m, nil
		}
		m.name, m.notice = msg.lookup.name, msg.lookup.notice
		if msg.err != nil {
			m.notice = msg.err.Error()
			if msg.saving {
				m.state = "invalid"
			} else {
				m.state = "manual"
			}
		} else if msg.lookup.invalid {
			m.state = "invalid"
		} else if msg.lookup.missing {
			m.state = "review"
		} else if msg.lookup.id != "" {
			m.id, m.done = msg.lookup.id, true
			return m, tea.Quit
		} else {
			m.state = "manual"
		}
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.cancel()
			m.err = context.Canceled
			return m, tea.Quit
		}
		if m.state == "lookup" && msg.String() == "esc" {
			m.cancel()
			m.state, m.notice = "manual", "Verification skipped. Enter the UUID manually; valid tunnel-credentials must exist before deployment."
			return m, nil
		}
		if m.state == "invalid" && msg.String() == "r" {
			m.state = "lookup"
			return m, m.request(false)
		}
		if m.state == "manual" {
			if msg.String() == "enter" {
				if err := validateCloudflareTunnelID(m.manual.Value()); err != nil {
					m.notice = err.Error()
					return m, nil
				}
				m.id, m.done = m.manual.Value(), true
				return m, tea.Quit
			}
			var cmd tea.Cmd
			m.manual, cmd = m.manual.Update(msg)
			return m, cmd
		}
		if m.state == "review" {
			if msg.Paste {
				m.editor.SetValue(string(msg.Runes))
				m.notice = ""
				return m, nil
			}
			switch msg.String() {
			case "e":
				m.state = "edit"
				return m, m.editor.Focus()
			case "enter":
				if _, err := parseCloudflareCredentials(m.editor.Value()); err != nil {
					m.notice = err.Error()
					return m, nil
				}
				m.state = "saving"
				return m, m.request(true)
			}
			return m, nil
		}
		if m.state == "edit" && (msg.String() == "esc" || msg.String() == "ctrl+s") {
			m.editor.Blur()
			m.state, m.notice = "review", ""
			return m, nil
		}
	}
	if m.state == "edit" {
		var cmd tea.Cmd
		m.editor, cmd = m.editor.Update(msg)
		return m, cmd
	}
	return m, nil
}

// View renders credentials masked unless the user explicitly enters edit mode.
func (m cloudflareForm) View() string {
	if m.done || m.err != nil {
		return ""
	}
	title := lipgloss.NewStyle().Bold(true).Render("Cloudflare Tunnels credentials")
	body, help := "Checking credentials secret…", "Esc: skip verification · Ctrl-C: cancel"
	switch m.state {
	case "saving":
		body, help = "Saving credentials secret…", "Ctrl-C: cancel"
	case "invalid":
		body, help = "", "r: retry lookup · Ctrl-C: cancel"
	case "manual":
		body, help = "Tunnel UUID\n"+m.manual.View(), "Enter: continue · Ctrl-C: cancel"
	case "edit":
		body, help = "Secret visible while editing\n"+m.editor.View(), "Ctrl-S/Esc: review · Ctrl-C: cancel"
	case "review":
		help = "Paste: replace · e: edit (reveals secret)\nEnter: create secret · Ctrl-C: cancel"
		if strings.TrimSpace(m.editor.Value()) == "" {
			body = "Paste the credentials JSON from cloudflared tunnel create (not a token).\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(cloudflareCredentialPlaceholder)
		} else if value, err := parseCloudflareCredentials(m.editor.Value()); err == nil {
			body = "AccountTag: " + value.AccountTag + "\nTunnelID: " + value.TunnelID + "\nTunnelSecret: ••••••••"
		} else {
			body = "Credentials pasted. " + err.Error() + "\nChoose Edit to correct the JSON."
		}
	}
	parts := []string{title}
	if m.name != "" {
		parts = append(parts, "Secret: "+m.name)
	}
	if body != "" {
		parts = append(parts, body)
	}
	if m.notice != "" {
		parts = append(parts, m.notice)
	}
	parts = append(parts, help)
	return ansi.Wrap(strings.Join(parts, "\n\n"), max(1, m.width-2), "")
}
