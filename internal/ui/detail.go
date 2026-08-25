package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jhonsanchez/standup/internal/data"
	"github.com/jhonsanchez/standup/internal/github"
	"github.com/jhonsanchez/standup/internal/jira"
)

// detailState is one entry in the detail navigation stack, so you can jump
// issue → PR → back without losing scroll position or fetched data.
type detailState struct {
	item     data.Item
	pr       *data.PRDetail
	comments []data.Comment
	loading  bool
	err      string
	scroll   int

	// GHA checks window (pushed with C).
	isChecks   bool
	checksSHA  string
	checksKind string // "pre-merge" | "post-merge"
	runs       []data.WorkflowRun
}

type prDetailMsg struct {
	url    string
	detail *data.PRDetail
	err    error
}

type jiraDetailMsg struct {
	url      string
	item     data.Item
	comments []data.Comment
	err      error
}

type checksMsg struct {
	url  string
	runs []data.WorkflowRun
	err  error
}

type checksTickMsg struct {
	url string
}

type checkoutMsg struct {
	msg string
	err error
}

type execDoneMsg struct {
	name string
	err  error
}

func (m *Model) top() *detailState {
	if len(m.detailStack) == 0 {
		return nil
	}
	return &m.detailStack[len(m.detailStack)-1]
}

func (m *Model) stateFor(url string) *detailState {
	for i := range m.detailStack {
		if m.detailStack[i].item.URL == url {
			return &m.detailStack[i]
		}
	}
	return nil
}

func (m *Model) openDetail(it data.Item) (tea.Model, tea.Cmd) {
	if t := m.top(); t != nil && t.item.URL == it.URL {
		return *m, nil
	}
	st := detailState{item: it}
	m.pick = nil
	m.status = ""
	client := m.cfg.Clients[m.client]

	var cmd tea.Cmd
	switch it.Kind {
	case data.KindPullRequest:
		if client.GitHub != nil {
			st.loading = true
			g := client.GitHub
			url, repo, num := it.URL, it.Repo, prNumber(it.Key)
			cmd = func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				d, err := github.FetchPRDetail(ctx, g, repo, num)
				return prDetailMsg{url: url, detail: d, err: err}
			}
		}
	case data.KindJiraIssue:
		if client.Jira != nil {
			st.loading = true
			j := client.Jira
			url, key := it.URL, it.Key
			cmd = func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				item, err := jira.FetchIssue(ctx, j, key)
				if err != nil {
					return jiraDetailMsg{url: url, err: err}
				}
				comments, err := jira.FetchComments(ctx, j, key)
				return jiraDetailMsg{url: url, item: item, comments: comments, err: err}
			}
		}
	}
	m.detailStack = append(m.detailStack, st)
	return *m, cmd
}

// openChecks pushes the GHA checks window for a PR onto the detail stack.
func (m *Model) openChecks(pr data.Item) (tea.Model, tea.Cmd) {
	sha, kind := pr.HeadSHA, "pre-merge"
	if pr.Merged && pr.MergeSHA != "" {
		sha, kind = pr.MergeSHA, "post-merge"
	}
	if sha == "" {
		m.status = "no commit SHA known for " + pr.Key + " — refresh (r) and retry"
		return *m, nil
	}
	st := detailState{item: pr, isChecks: true, checksSHA: sha, checksKind: kind, loading: true}
	st.item.URL = pr.URL + "/checks" // distinct URL: browser target + stack identity
	m.pick = nil
	m.status = ""
	m.detailStack = append(m.detailStack, st)
	return *m, m.fetchChecksCmd(st.item.URL, pr.Repo, sha)
}

func (m *Model) fetchChecksCmd(url, repo, sha string) tea.Cmd {
	g := m.cfg.Clients[m.client].GitHub
	if g == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		runs, err := github.FetchChecks(ctx, g, repo, sha)
		return checksMsg{url: url, runs: runs, err: err}
	}
}

func runsPending(runs []data.WorkflowRun) bool {
	for _, r := range runs {
		if r.Status != "COMPLETED" {
			return true
		}
	}
	return false
}

func prNumber(key string) int {
	var n int
	if i := strings.LastIndex(key, "#"); i >= 0 {
		fmt.Sscanf(key[i+1:], "%d", &n)
	}
	return n
}

var (
	defaultIssueKeyRe = regexp.MustCompile(`[A-Z][A-Z0-9]+-\d+`)
	issueKeyRe        = defaultIssueKeyRe
)

// jumpTargets returns where `p` can go from the current detail: an issue's
// linked PRs, or a PR's referenced Jira issue.
func (m *Model) jumpTargets() []data.Item {
	t := m.top()
	if t == nil {
		return nil
	}
	it := t.item
	if it.Kind == data.KindJiraIssue {
		return m.linkedPRs(it.Key)
	}
	// PR → issue: find the key in title or branch among fetched issues.
	keys := issueKeyRe.FindAllString(it.Title+" "+it.Branch, -1)
	var out []data.Item
	for _, k := range keys {
		for _, is := range m.states[m.client].issues {
			if is.Key == k {
				out = append(out, is)
			}
		}
	}
	return out
}

func (m Model) updateDetail(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case prDetailMsg:
		if st := m.stateFor(msg.url); st != nil {
			st.loading = false
			if msg.err != nil {
				st.err = msg.err.Error()
			} else {
				st.pr = msg.detail
			}
		}
		return m, nil

	case jiraDetailMsg:
		if st := m.stateFor(msg.url); st != nil {
			st.loading = false
			if msg.err != nil {
				st.err = msg.err.Error()
			}
			if msg.item.Key != "" {
				st.item = msg.item
			}
			st.comments = msg.comments
		}
		return m, nil

	case checksMsg:
		if st := m.stateFor(msg.url); st != nil {
			st.loading = false
			if msg.err != nil {
				st.err = msg.err.Error()
			} else {
				st.err = ""
				st.runs = msg.runs
			}
			// Watch live: refetch every 10s while any run is pending.
			if runsPending(st.runs) {
				url := msg.url
				return m, tea.Tick(10*time.Second, func(time.Time) tea.Msg {
					return checksTickMsg{url: url}
				})
			}
		}
		return m, nil

	case checksTickMsg:
		if st := m.stateFor(msg.url); st != nil && st.isChecks {
			return m, m.fetchChecksCmd(msg.url, st.item.Repo, st.checksSHA)
		}
		return m, nil

	case checkoutMsg:
		if msg.err != nil {
			m.status = "checkout failed: " + msg.err.Error()
		} else {
			m.status = msg.msg
		}
		return m, nil

	case execDoneMsg:
		if msg.err != nil {
			m.status = msg.name + ": " + msg.err.Error()
		} else {
			m.status = "back from " + msg.name
		}
		return m, nil

	case tea.KeyMsg:
		// PR/issue picker overlay (multiple jump targets).
		if len(m.pick) > 0 {
			s := msg.String()
			if s == "esc" {
				m.pick = nil
				return m, nil
			}
			if s >= "1" && s <= "9" {
				n := int(s[0] - '1')
				if n < len(m.pick) {
					it := m.pick[n]
					m.pick = nil
					return m.openDetail(it)
				}
			}
			return m, nil
		}

		// gg → top (vim). A pending g followed by anything else falls through.
		if m.pendingG {
			m.pendingG = false
			if msg.String() == "g" {
				m.top().scroll = 0
				return m, nil
			}
		}

		// In the checks window, digits open the numbered run in the browser.
		if t := m.top(); t != nil && t.isChecks {
			s := msg.String()
			if s >= "1" && s <= "9" {
				if n := int(s[0] - '1'); n < len(t.runs) && t.runs[n].URL != "" {
					m.status = "opened " + t.runs[n].Name
					return m, openURLCmd(t.runs[n].URL)
				}
				return m, nil
			}
		}

		switch {
		case m.km.Is(msg, "back"):
			m.detailStack = m.detailStack[:len(m.detailStack)-1]
			m.status = ""
			return m, nil
		case m.km.Is(msg, "to-list"):
			m.detailStack = nil
			m.status = ""
			return m, nil
		case m.km.Is(msg, "help"):
			m.showHelp = true
			return m, nil
		case msg.String() == "ctrl+c":
			return m, tea.Quit
		case m.km.Is(msg, "down"):
			m.top().scroll++
			return m, nil
		case m.km.Is(msg, "up"):
			if t := m.top(); t.scroll > 0 {
				t.scroll--
			}
			return m, nil
		case msg.String() == "g":
			m.pendingG = true
			return m, nil
		case m.km.Is(msg, "bottom"):
			m.top().scroll = 1 << 30 // clamped to bottom at render
			return m, nil
		case m.km.Is(msg, "page"):
			page := m.termHeight() - 8
			if page < 4 {
				page = 4
			}
			delta := page
			switch msg.String() {
			case "ctrl+d", "ctrl+u":
				delta = page / 2
			}
			switch msg.String() {
			case "ctrl+u", "ctrl+b", "pgup":
				delta = -delta
			}
			t := m.top()
			t.scroll += delta // clamped at render
			if t.scroll < 0 {
				t.scroll = 0
			}
			return m, nil
		case m.km.Is(msg, "top"):
			m.top().scroll = 0
			return m, nil
		case m.km.Is(msg, "open"), msg.String() == "enter":
			it := m.top().item
			m.status = "opened " + it.URL
			return m, openURLCmd(it.URL)
		case m.km.Is(msg, "copy"):
			it := m.top().item
			m.copyURL(it.URL, it.Key)
			return m, nil
		case m.km.Is(msg, "copy-linked"):
			it := m.top().item
			if cp := m.counterpart(it, it.Key); cp != nil {
				m.copyURL(cp.URL, cp.Key)
			} else {
				m.status = "no linked item for " + it.Key
			}
			return m, nil
		case m.km.Is(msg, "jump"):
			targets := m.jumpTargets()
			switch len(targets) {
			case 0:
				if m.top().item.Kind == data.KindJiraIssue {
					m.status = "no open PR references " + m.top().item.Key
				} else {
					m.status = "no sprint issue matches this PR"
				}
				return m, nil
			case 1:
				return m.openDetail(targets[0])
			default:
				m.pick = targets
				return m, nil
			}
		case m.km.Is(msg, "checks"):
			t := m.top()
			pr := t.item
			if pr.Kind != data.KindPullRequest {
				if lp := m.linkedPR(pr.Key); lp != nil {
					pr = *lp
				} else {
					m.status = "no linked PR for " + pr.Key
					return m, nil
				}
			}
			return m.openChecks(pr)
		case m.km.Is(msg, "checkout"):
			return m, m.checkoutCmd()
		case m.km.Is(msg, "git-ui"):
			return m, m.execInRepo("git UI", m.cfg.GitUICommand())
		case m.km.Is(msg, "terminal"):
			shell := os.Getenv("SHELL")
			if shell == "" {
				shell = "sh"
			}
			return m, m.execInRepo("terminal", shell)
		case m.km.Is(msg, "agent"):
			it := m.top().item
			prompt := fmt.Sprintf("I'm working on %s: %s (%s). Help me with it.", it.Key, it.Title, it.URL)
			argv := m.cfg.AgentCommand(prompt)
			return m, m.execInRepo("agent", argv[0], argv[1:]...)
		}
	}
	return m, nil
}

// detailTarget resolves the git repo/branch behind the current detail item —
// the PR itself, or the first PR linked to the Jira issue.
func (m *Model) detailTarget() (repo, branch string, ok bool) {
	t := m.top()
	if t == nil {
		return "", "", false
	}
	it := t.item
	if it.Kind == data.KindPullRequest {
		branch = it.Branch
		if branch == "" && t.pr != nil {
			branch = t.pr.Branch
		}
		return it.Repo, branch, true
	}
	if pr := m.linkedPR(it.Key); pr != nil {
		return pr.Repo, pr.Branch, true
	}
	if brs := m.localBranches(it.Key); len(brs) > 0 {
		return brs[0].Repo, brs[0].Name, true
	}
	return "", "", false
}

// execInRepo suspends the TUI and runs an interactive program in the item's
// local clone; the TUI resumes exactly where it was when the program exits.
func (m *Model) execInRepo(name, prog string, args ...string) tea.Cmd {
	repo, _, ok := m.detailTarget()
	if !ok {
		m.status = "no linked PR/repo for this item"
		return nil
	}
	dir, exists := m.cfg.Clients[m.client].RepoDir(repo)
	if !exists {
		m.status = fmt.Sprintf("no local clone at %s", dir)
		return nil
	}
	if _, err := exec.LookPath(prog); err != nil {
		hint := ""
		switch prog {
		case "lazygit":
			hint = " — install: brew install lazygit (github.com/jesseduffield/lazygit)"
		case "claude":
			hint = " — install: https://claude.com/claude-code"
		case "copilot":
			hint = " — install: https://github.com/github/copilot-cli"
		}
		m.status = "✗ " + prog + " not found in PATH" + hint
		return nil
	}
	c := exec.Command(prog, args...)
	c.Dir = dir
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return execDoneMsg{name: name, err: err}
	})
}

// checkoutCmd switches the local clone to the item's branch.
func (m *Model) checkoutCmd() tea.Cmd {
	repo, branch, ok := m.detailTarget()
	if !ok || branch == "" {
		m.status = "no branch known for this item"
		return nil
	}
	dir, exists := m.cfg.Clients[m.client].RepoDir(repo)
	if !exists {
		m.status = fmt.Sprintf("no local clone at %s", dir)
		return nil
	}
	m.status = fmt.Sprintf("checking out %s in %s…", branch, dir)
	return func() tea.Msg {
		if out, err := exec.Command("git", "-C", dir, "fetch", "origin").CombinedOutput(); err != nil {
			return checkoutMsg{err: fmt.Errorf("fetch: %s", firstLine(out))}
		}
		if err := exec.Command("git", "-C", dir, "switch", branch).Run(); err != nil {
			if out, err2 := exec.Command("git", "-C", dir, "switch", "-c", branch, "--track", "origin/"+branch).CombinedOutput(); err2 != nil {
				return checkoutMsg{err: fmt.Errorf("switch: %s", firstLine(out))}
			}
		}
		return checkoutMsg{msg: fmt.Sprintf("✓ %s now on %s", dir, branch)}
	}
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

func (m Model) viewDetail() string {
	t := m.top()
	it := t.item
	w := m.width - 4
	if w < 40 {
		w = 40
	}
	wrap := lipgloss.NewStyle().Width(w)

	var lines []string
	push := func(s string) {
		lines = append(lines, strings.Split(wrap.Render(s), "\n")...)
	}
	pushComments := func(comments []data.Comment) {
		if len(comments) == 0 {
			return
		}
		push("")
		push(headerLabel.Render(fmt.Sprintf("Comments (%d)", len(comments))))
		for _, c := range comments {
			push("  " + keyStyle.Render(c.Author) + " " + subtaskStyle.Render(relAge(c.Created)+" ago"))
			for _, l := range strings.Split(c.Body, "\n") {
				push("    " + l)
			}
		}
	}

	// Pinned header: breadcrumb + "KEY — Title", always visible while
	// the content below scrolls.
	var pinned []string
	if len(m.detailStack) > 1 {
		var crumbs []string
		for _, s := range m.detailStack {
			crumbs = append(crumbs, s.item.Key)
		}
		pinned = append(pinned, subtaskStyle.MaxWidth(w).Render(strings.Join(crumbs, " → ")))
	}
	pinned = append(pinned,
		lipgloss.NewStyle().MaxWidth(w).Render(headerLabel.Render(it.Key)+" — "+it.Title),
		subtaskStyle.Render(strings.Repeat("─", w)))

	// Scrollable content.
	switch it.Kind {
	case data.KindJiraIssue:
		meta := statusBadge(it.StatusCategory, it.Status)
		if it.IssueType != "" {
			meta += "  " + subtaskStyle.Render(it.IssueType)
		}
		if it.Priority != "" {
			meta += "  " + subtaskStyle.Render(it.Priority)
		}
		push(meta)
	case data.KindPullRequest:
		meta := bucketDot(it.Bucket) + " " + it.Bucket.Label() +
			"  " + ciIcon(it.CIState) + conflictIcon(it.Mergeable) +
			"  " + diffStat(it.Additions, it.Deletions)
		if rl := reviewLabel(it.ReviewDecision); rl != "" {
			meta += "  " + rl
		}
		if it.Author != "" {
			meta += "  " + subtaskStyle.Render("@"+it.Author)
		}
		push(meta)
	}
	if repo, branch, ok := m.detailTarget(); ok && branch != "" {
		dir, exists := m.cfg.Clients[m.client].RepoDir(repo)
		local := errStyle.Render(" (no local clone)")
		if exists {
			local = subtaskStyle.Render(" → " + dir)
		}
		push(keyStyle.Render(branchGlyph+" "+branch) + local)
	}
	push(subtaskStyle.Render(it.URL))
	push("")

	if t.loading {
		push(m.spin.View() + " loading detail…")
	}
	if t.err != "" {
		push(errStyle.Render("⚠ " + t.err))
	}

	if t.isChecks {
		m.pushChecks(t, push)
	}
	// For a checks state the Kind switch below is inert (t.pr is nil).

	switch it.Kind {
	case data.KindJiraIssue:
		if desc := strings.TrimSpace(it.Description); desc != "" {
			push(desc)
			push("")
		}
		prs := m.linkedPRs(it.Key)
		if len(prs) == 0 {
			if brs := m.localBranches(it.Key); len(brs) > 0 {
				push(headerLabel.Render("Git"))
				for _, b := range brs {
					push("  " + keyStyle.Render(branchGlyph+" "+b.Name) +
						subtaskStyle.Render(" → "+b.RepoDir+" · no PR yet"))
				}
				push("")
			}
		}
		if len(prs) > 0 {
			push(headerLabel.Render("Git") + subtaskStyle.Render("  (p to open)"))
			for _, p := range prs {
				var line string
				if p.Merged {
					line = "  " + mergedStyle.Render(mergedGlyph+" "+p.Key) +
						" merged " + relAge(p.MergedAt) + " ago · post-merge " + ciIcon(p.MergeCIState)
				} else {
					line = "  " + bucketDot(p.Bucket) + " " + keyStyle.Render(p.Key) + " " +
						ciIcon(p.CIState) + conflictIcon(p.Mergeable) + " " +
						diffStat(p.Additions, p.Deletions) + " " +
						subtaskStyle.Render(branchGlyph+" "+p.Branch) + " · " + p.Bucket.Label()
					if rl := reviewLabel(p.ReviewDecision); rl != "" {
						line += " · " + rl
					}
				}
				push(line)
			}
			push("")
		}
		if len(it.Subtasks) > 0 {
			push(headerLabel.Render(fmt.Sprintf("Subtasks (%d)", len(it.Subtasks))))
			for _, s := range it.Subtasks {
				line := "  " + statusBadge(s.StatusCategory, s.Status) + " " + keyStyle.Render(s.Key)
				if mark := gitMarker(m.linkedPR(s.Key)); mark != "" {
					line += " " + mark
				}
				push(line + " " + s.Summary)
			}
		}
		pushComments(t.comments)

	case data.KindPullRequest:
		if d := t.pr; d != nil {
			if body := strings.TrimSpace(d.Body); body != "" {
				push(body)
				push("")
			}
			if len(d.Checks) > 0 {
				push(headerLabel.Render(fmt.Sprintf("Checks & Deployments (%d)", len(d.Checks))))
				for _, c := range d.Checks {
					push("  " + ciIcon(c.State) + " " + c.Name + " " + subtaskStyle.Render(strings.ToLower(c.State)))
				}
				push("")
			}
			push(headerLabel.Render(fmt.Sprintf("Commits (%d)", len(d.Commits))))
			for _, c := range d.Commits {
				push("  " + subtaskStyle.Render(c.SHA) + " " + c.Message + " " + subtaskStyle.Render("· "+c.Author))
			}
			push("")
			push(headerLabel.Render(fmt.Sprintf("Files (%d)", len(d.Files))))
			for _, f := range d.Files {
				push("  " + diffStat(f.Additions, f.Deletions) + " " + f.Path)
			}
			pushComments(d.Comments)
		}
	}

	// Pinned footer: picker + status + help.
	var fparts []string
	if len(m.pick) > 0 {
		fparts = append(fparts, headerLabel.Render("Jump to:"))
		for i, p := range m.pick {
			fparts = append(fparts, fmt.Sprintf("  %s %s %s",
				keyStyle.Render(fmt.Sprintf("%d)", i+1)), p.Key, p.Title))
		}
	}
	if m.status != "" {
		fparts = append(fparts, helpStyle.MaxWidth(m.width).Render(m.status))
	}
	fparts = append(fparts, helpStyle.MaxWidth(m.width).Render(m.detailHelp()))
	footer := strings.Join(fparts, "\n")

	// Scroll window fills the space between pinned header and footer.
	avail := m.termHeight() - len(pinned) - lipgloss.Height(footer) - 1
	if avail < 5 {
		avail = 5
	}
	scroll := t.scroll
	if scroll > len(lines)-avail {
		scroll = len(lines) - avail
	}
	if scroll < 0 {
		scroll = 0
	}
	t.scroll = scroll // clamp overscroll so j/k stay responsive
	end := scroll + avail
	if end > len(lines) {
		end = len(lines)
	}
	if end < len(lines) || scroll > 0 {
		hint := fmt.Sprintf("scroll %d-%d of %d", scroll+1, end, len(lines))
		footer = helpStyle.MaxWidth(m.width).Render(hint) + "\n" + footer
		avail--
		if end > scroll+avail {
			end = scroll + avail
		}
	}

	body := padToHeight(strings.Join(lines[scroll:end], "\n"), avail)
	return lipgloss.NewStyle().Padding(0, 1).Render(
		strings.Join(pinned, "\n") + "\n" + body + "\n" + footer)
}

// detailHelp is the minimal pinned footer for detail views; the full menu
// lives behind `?`.
func (m Model) detailHelp() string {
	if len(m.pick) > 0 {
		return "1-9 pick · esc cancel"
	}
	if t := m.top(); t != nil && t.isChecks {
		return "1-9 open run · " + m.km.label("back") + " back · " + m.km.label("help") + " help"
	}
	parts := []string{m.km.label("back") + " back"}
	if t := m.top(); t != nil &&
		(t.item.Kind == data.KindPullRequest || m.linkedPR(t.item.Key) != nil) {
		parts = append(parts, m.km.label("checks")+" checks")
	}
	if targets := m.jumpTargets(); len(targets) > 0 {
		if m.top().item.Kind == data.KindJiraIssue {
			parts = append(parts, m.km.label("jump")+" → PR")
		} else {
			parts = append(parts, m.km.label("jump")+" → issue")
		}
	}
	parts = append(parts, m.km.label("help")+" help")
	return strings.Join(parts, " · ")
}

// pushChecks renders the GHA checks window content.
func (m Model) pushChecks(t *detailState, push func(string)) {
	push(subtaskStyle.Render(fmt.Sprintf("%s checks · commit %.10s", t.checksKind, t.checksSHA)))
	push("")
	if len(t.runs) == 0 && !t.loading && t.err == "" {
		push(subtaskStyle.Render("no checks found for this commit"))
		return
	}
	for i, r := range t.runs {
		age := ""
		if !r.Created.IsZero() {
			age = relAge(r.Created) + " ago"
		}
		push(fmt.Sprintf("%s %s %s %s",
			keyStyle.Render(fmt.Sprintf("%d)", i+1)),
			runIcon(r.Status, r.Conclusion),
			headerLabel.Render(r.Name),
			subtaskStyle.Render(age)))
		for _, j := range r.Jobs {
			dur := ""
			if !j.Started.IsZero() && !j.Completed.IsZero() {
				dur = j.Completed.Sub(j.Started).Round(time.Second).String()
			}
			push(fmt.Sprintf("     %s %s %s",
				runIcon(j.Status, j.Conclusion), j.Name, subtaskStyle.Render(dur)))
		}
		push("")
	}
	if runsPending(t.runs) {
		push(m.spin.View() + subtaskStyle.Render(" runs in progress — auto-refreshing every 10s"))
	}
}

func runIcon(status, conclusion string) string {
	if status != "COMPLETED" {
		return iconPending
	}
	switch conclusion {
	case "SUCCESS":
		return iconPass
	case "FAILURE", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
		return iconFail
	default: // CANCELLED, SKIPPED, NEUTRAL, …
		return iconNone
	}
}

func openURLCmd(url string) tea.Cmd {
	return func() tea.Msg {
		_ = exec.Command(openCommand, url).Start()
		return nil
	}
}
