// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// SelectList asks the user to choose one item from a terminal list.
func SelectList(action string, title string, items []list.Item) (string, bool, error) {
	l := list.New(items, itemDelegate{}, listWidth, listHeight)
	l.Title = title
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = lipgloss.NewStyle().Background(ColorPrimary).Foreground(ColorWhite)
	l.Styles.PaginationStyle = list.DefaultStyles().PaginationStyle
	l.Styles.HelpStyle = list.DefaultStyles().HelpStyle.PaddingBottom(1)

	m, err := tea.NewProgram(model{list: l, action: action}).Run()
	if err != nil {
		return "", false, fmt.Errorf("error running program: %w", err)
	}

	if m, ok := m.(model); ok {
		return m.choice, m.quitting, nil
	}
	return "", true, fmt.Errorf("error processing selection")
}

// SelectString asks the user to choose one string value from a terminal list.
func SelectString(action string, title string, values []string) (string, bool, error) {
	items := make([]list.Item, 0, len(values)+1)
	for _, value := range values {
		items = append(items, Item{Key: value, Label: value})
	}
	items = append(items, Item{Key: "", Label: "Cancel", Cancel: true})
	return SelectList(action, title, items)
}

const listWidth = 20
const listHeight = 14

// Item is one selectable list entry.
type Item struct {
	Key    string
	Label  string
	Cancel bool
}

// FilterValue returns the searchable value for the list item.
func (i Item) FilterValue() string { return "" }

type itemDelegate struct{}

func (d itemDelegate) Height() int                             { return 1 }
func (d itemDelegate) Spacing() int                            { return 0 }
func (d itemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

var itemStyle = lipgloss.NewStyle().PaddingLeft(2)
var selectedItemStyle = lipgloss.NewStyle().Foreground(ColorSecondary)

func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(Item)
	if !ok {
		return
	}

	str := fmt.Sprintf("%d. %s", index+1, i.Label)

	fn := itemStyle.Render
	if index == m.Index() {
		fn = func(s ...string) string {
			return selectedItemStyle.Render("> " + strings.Join(s, " "))
		}
	}

	_, _ = fmt.Fprint(w, fn(str))
}

type model struct {
	action   string
	list     list.Model
	choice   string
	quitting bool
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetWidth(msg.Width)
		return m, nil

	case tea.KeyMsg:
		switch keypress := msg.String(); keypress {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		case "enter":
			i, ok := m.list.SelectedItem().(Item)
			if ok && i.Cancel {
				m.quitting = true
			} else if ok {
				m.choice = i.Key
			}
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if m.quitting || m.choice != "" {
		// clears the window of selection output
		return "\n"
	}
	return "\n" + m.list.View()
}
