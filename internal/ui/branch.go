package ui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jhonsanchez/standup/internal/data"
	"github.com/jhonsanchez/standup/internal/gitscan"
)

// branchConfirm is the one confirmation step before creating + pushing the
// mapping branch (pushing is outward-facing).
type branchConfirm struct {
	item   data.Item
	opt    repoOption
	base   string
	branch string
}

type branchDoneMsg struct {
	key     string
	repo    string
	dir     string
	branch  string
	existed bool
	pushErr error
	err     error
}

// branchName applies the optional branch_template (default: the bare key).
func (m *Model) branchName(it data.Item) string {
	t := m.cfg.BranchTemplate
	if t == "" {
		t = "{key}"
	}
	return strings.ReplaceAll(t, "{key}", it.Key)
}

// suggestRepo guesses the repo for an unlinked issue: a clone name mentioned
// in the title/description (longest match wins), else the repo_map default.
func (m *Model) suggestRepo(it data.Item) string {
	text := strings.ToLower(it.Title + " " + it.Description)
	best := ""
	for _, r := range m.repoOptions() {
		n := strings.ToLower(r.name)
		if strings.Contains(text, n) && len(n) > len(best) {
			best = r.name
		}
	}
	if best != "" {
		return best
	}
	if proj, _, ok := strings.Cut(it.Key, "-"); ok {
		if r := m.cfg.Clients[m.client].RepoMap[proj]; r != "" {
			return r
		}
	}
	return ""
}

// openStartBranch begins the b flow: pick a repo (suggestion preselected),
// then confirm create+push.
func (m *Model) openStartBranch(it data.Item) (tea.Model, tea.Cmd) {
	repos := m.repoOptions()
	if len(repos) == 0 {
		m.status = "no local clones found under " + m.cfg.Clients[m.client].ProjectsBase()
		return *m, nil
	}
	suggest := m.suggestRepo(it)
	// Suggestion first, rest alphabetical (repoOptions is already sorted).
	if suggest != "" {
		sort.SliceStable(repos, func(a, b int) bool {
			return repos[a].name == suggest && repos[b].name != suggest
		})
	}
	ti := newPickInput()
	m.repoPick = &repoPickState{item: it, input: ti, repos: repos, suggest: suggest, forBranch: true}
	return *m, nil
}

// confirmBranch moves from the picker to the single confirm line.
func (m Model) confirmBranch(it data.Item, opt repoOption) (tea.Model, tea.Cmd) {
	m.repoPick = nil
	m.branchOp = &branchConfirm{
		item:   it,
		opt:    opt,
		base:   gitscan.DefaultBase(opt.dir),
		branch: (&m).branchName(it),
	}
	return m, nil
}

func (m Model) handleBranchConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	bc := m.branchOp
	switch msg.String() {
	case "enter":
		m.branchOp = nil
		m.status = m.spin.View() + " creating " + bc.branch + " in " + bc.opt.name + "…"
		return m, func() tea.Msg {
			existed, pushErr, err := gitscan.StartBranch(bc.opt.dir, bc.branch)
			return branchDoneMsg{
				key: bc.item.Key, repo: bc.opt.name, dir: bc.opt.dir,
				branch: bc.branch, existed: existed, pushErr: pushErr, err: err,
			}
		}
	case "esc":
		m.branchOp = nil
		return m, nil
	}
	return m, nil
}

func (m Model) applyBranchDone(msg branchDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = "✗ " + msg.err.Error()
		return m, nil
	}
	// Link immediately — don't wait for the next scan.
	st := &m.states[m.client]
	st.branches = append(st.branches, data.BranchRef{Repo: msg.repo, RepoDir: msg.dir, Name: msg.branch})
	switch {
	case msg.existed:
		m.status = fmt.Sprintf("✓ %s already exists on origin (%s) — linked · c checkout · A chat", msg.branch, msg.repo)
	case msg.pushErr != nil:
		m.status = fmt.Sprintf("⚠ %s created locally in %s, but %v — mapping works on this machine only", msg.branch, msg.repo, msg.pushErr)
	default:
		m.status = fmt.Sprintf("✓ %s pushed to %s — c checkout · A chat · a claude", msg.branch, msg.repo)
	}
	return m, nil
}

func (m Model) viewBranchConfirm() string {
	bc := m.branchOp
	var b strings.Builder
	b.WriteString(headerLabel.Render("Start work on "+bc.item.Key) + "\n\n")
	b.WriteString("  create branch " + keyStyle.Render(bc.branch) +
		" from " + subtaskStyle.Render(bc.base) +
		" in " + keyStyle.Render(bc.opt.name) + " and push to origin?\n\n")
	b.WriteString(subtaskStyle.Render("  your working tree in "+bc.opt.dir+" is not touched — checkout stays a separate step (c)") + "\n\n")
	b.WriteString(helpStyle.Render("enter create & push · esc cancel"))
	return padToHeight(lipgloss.NewStyle().Padding(0, 1).Render(b.String()), m.termHeight())
}
