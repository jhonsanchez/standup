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

// InjectTestBranches seeds the active client's branch scan (test helper).
func InjectTestBranches(model tea.Model, branches []data.BranchRef) tea.Model {
	m := model.(Model)
	m.states[m.client].branches = branches
	return m
}

// InjectTestMerged seeds the active client's merged-PR list (test helper).
func InjectTestMerged(model tea.Model, merged []data.Item) tea.Model {
	m := model.(Model)
	m.states[m.client].merged = merged
	return m
}

// InjectTestPRs seeds the active client's open-PR list (test helper).
func InjectTestPRs(model tea.Model, prs []data.Item) tea.Model {
	m := model.(Model)
	m.states[m.client].prs = prs
	return m
}
