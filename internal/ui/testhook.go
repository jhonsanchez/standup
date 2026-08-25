package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jhonsanchez/standup/internal/data"
)

// InjectTestData seeds the active client's issue list (test helper).
func InjectTestData(model tea.Model, issues []data.Item) tea.Model {
	m := model.(Model)
	m.states[m.client].loaded = true
	m.states[m.client].issues = issues
	return m
}
