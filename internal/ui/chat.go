package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jhonsanchez/standup/internal/chat"
	"github.com/jhonsanchez/standup/internal/data"
	"github.com/jhonsanchez/standup/internal/jirafmt"
)

const (
	chatRoleUser = iota
	chatRoleAssistant
	chatRoleTool
	chatRoleSystem
)

type chatMsg struct {
	role int
	text string
}

// chatSession is the per-issue conversation. It outlives the view (sessions
// map on the model), so esc + reopen resumes where you left off.
type chatSession struct {
	item     data.Item
	itemKey  string
	repoDir  string
	claudeID string // claude session for --resume
	msgs     []chatMsg
	events   chan chat.Event
	cancel   func()
	running  bool
	scroll   int // lines scrolled up from the bottom
}

type chatEvMsg struct {
	key string
	ev  chat.Event
}

var (
	chatYouStyle    = lipgloss.NewStyle().Foreground(colGreen).Bold(true)
	chatClaudeStyle = lipgloss.NewStyle().Foreground(colPurple).Bold(true)
	chatToolStyle   = lipgloss.NewStyle().Foreground(colDim)
)

// openChat pushes the chat view for an item onto the detail stack, asking
// which repo to use when nothing links the item to one.
func (m *Model) openChat(it data.Item) (tea.Model, tea.Cmd) {
	cs := m.chats[it.Key]
	if cs == nil {
		dir, confident := m.chatRepoDir(it)
		if chosen := m.chatRepoChoice[it.Key]; chosen != "" {
			dir, confident = chosen, true
		}
		if !confident {
			if repos := m.repoOptions(); len(repos) > 0 {
				suggest := m.suggestRepo(it)
				if suggest != "" {
					sort.SliceStable(repos, func(a, b int) bool {
						return repos[a].name == suggest && repos[b].name != suggest
					})
				}
				m.repoPick = &repoPickState{item: it, input: newPickInput(), repos: repos, root: dir, suggest: suggest}
				return *m, textinput.Blink
			}
		}
		cs = m.newChatSession(it, dir)
	} else if upgraded, ok := m.chatRepoDir(it); ok && m.chatRepoChoice[it.Key] == "" && upgraded != cs.repoDir {
		// A PR/branch appeared since the chat was created — re-anchor.
		cs.repoDir = upgraded
		cs.msgs = append(cs.msgs, chatMsg{role: chatRoleSystem, text: "→ now running in " + upgraded})
	}
	m.pick = nil
	m.status = ""
	m.dock = &dockState{key: it.Key}
	m.chatInput.Focus()
	return *m, textarea.Blink
}

// dockState is the bottom chat panel: the upper view keeps rendering above
// it; while open, typing goes to the input.
type dockState struct {
	key string
}

func (m Model) dockHeight() int {
	if m.dock == nil {
		return 0
	}
	h := 14
	if half := m.termHeight() / 2; half < h {
		h = half
	}
	if h < 8 {
		h = 8
	}
	return h
}

// contentHeight is what the upper view may use when the dock is open.
func (m Model) contentHeight() int {
	return m.termHeight() - m.dockHeight()
}

func (m *Model) newChatSession(it data.Item, dir string) *chatSession {
	cs := &chatSession{item: it, itemKey: it.Key, repoDir: dir}
	cs.msgs = append(cs.msgs, chatMsg{role: chatRoleSystem, text: fmt.Sprintf(
		"chat about %s · claude runs in %s · mode %s, %d pre-approved tools (chat_allowed_tools adds more) — denied tools fail fast with a reason",
		it.Key, dir, m.cfg.ChatPermissionMode(), len(m.cfg.ChatTools(&m.cfg.Clients[m.client])))})
	m.chats[it.Key] = cs
	return cs
}

type repoOption struct {
	name string
	dir  string
}

type repoPickState struct {
	item      data.Item
	input     textinput.Model
	repos     []repoOption
	root      string
	suggest   string // preselected repo name (from title/repo_map)
	forBranch bool   // picking for the b start-work flow, not the chat
}

func newPickInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = "repo: "
	ti.Placeholder = "type to filter — enter picks the first match"
	ti.Focus()
	return ti
}

// repoOptions lists the client's local clones (from the branch scan — free).
func (m *Model) repoOptions() []repoOption {
	seen := map[string]bool{}
	var out []repoOption
	for _, b := range m.states[m.client].branches {
		if !seen[b.Repo] {
			seen[b.Repo] = true
			out = append(out, repoOption{name: b.Repo, dir: b.RepoDir})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].name < out[b].name })
	return out
}

func (rp *repoPickState) filtered() []repoOption {
	f := strings.ToLower(strings.TrimSpace(rp.input.Value()))
	if f == "" {
		return rp.repos
	}
	var out []repoOption
	for _, r := range rp.repos {
		if strings.Contains(strings.ToLower(r.name), f) {
			out = append(out, r)
		}
	}
	return out
}

// handleRepoPick processes keys while the chat repo picker is open.
func (m Model) handleRepoPick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rp := m.repoPick
	switch msg.String() {
	case "esc":
		m.repoPick = nil
		return m, nil
	case "enter":
		flt := rp.filtered()
		if rp.forBranch {
			if len(flt) == 0 {
				return m, nil
			}
			return m.confirmBranch(rp.item, flt[0])
		}
		dir := rp.root // chat: empty filter → projects root default
		if strings.TrimSpace(rp.input.Value()) != "" && len(flt) > 0 {
			dir = flt[0].dir
		} else if rp.suggest != "" && len(flt) > 0 && flt[0].name == rp.suggest {
			dir = flt[0].dir
		}
		return m.finishRepoPick(dir)
	default:
		if s := msg.String(); len(s) == 1 && s >= "1" && s <= "9" {
			if flt := rp.filtered(); len(flt) <= 9 || rp.input.Value() != "" {
				if n := int(s[0] - '1'); n < len(flt) {
					if rp.forBranch {
						return m.confirmBranch(rp.item, flt[n])
					}
					return m.finishRepoPick(flt[n].dir)
				}
			}
		}
	}
	var cmd tea.Cmd
	rp.input, cmd = rp.input.Update(msg)
	return m, cmd
}

func (m Model) finishRepoPick(dir string) (tea.Model, tea.Cmd) {
	rp := m.repoPick
	m.repoPick = nil
	m.chatRepoChoice[rp.item.Key] = dir
	m.newChatSession(rp.item, dir)
	return m.openChat(rp.item)
}

// viewRepoPick renders the chat repo chooser.
func (m Model) viewRepoPick() string {
	rp := m.repoPick
	var b strings.Builder
	if rp.forBranch {
		b.WriteString(headerLabel.Render("Start work on "+rp.item.Key+" — in which repo?") + "\n")
		b.WriteString(subtaskStyle.Render("a branch named after the issue will be created and pushed (maps the issue to the repo)") + "\n\n")
	} else {
		b.WriteString(headerLabel.Render("Where should Claude work on "+rp.item.Key+"?") + "\n")
		b.WriteString(subtaskStyle.Render("no local repo resolved for this item — pick one, enter with no filter = projects root ("+rp.root+") · tip: b maps an issue permanently via a branch") + "\n\n")
	}
	b.WriteString(rp.input.View() + "\n\n")
	flt := rp.filtered()
	limit := 9
	for i, r := range flt {
		if i >= limit {
			b.WriteString(subtaskStyle.Render(fmt.Sprintf("  … %d more — keep typing to narrow", len(flt)-limit)) + "\n")
			break
		}
		note := ""
		if r.name == rp.suggest {
			note = chatYouStyle.Render(" (suggested)")
		}
		b.WriteString(fmt.Sprintf("  %s %s%s %s\n",
			keyStyle.Render(fmt.Sprintf("%d)", i+1)), r.name, note, subtaskStyle.Render(r.dir)))
	}
	help := "1-9/enter pick · enter (empty) projects root · esc cancel"
	if rp.forBranch {
		help = "1-9/enter pick · esc cancel"
	}
	b.WriteString("\n" + helpStyle.Render(help))
	return padToHeight(lipgloss.NewStyle().Padding(0, 1).Render(b.String()), m.termHeight())
}

// chatRepoDir picks where claude runs: the linked PR's clone, a matching
// branch's clone, the repo_map default — or, unconfidently, the projects
// root (which triggers the repo picker).
func (m *Model) chatRepoDir(it data.Item) (string, bool) {
	client := m.cfg.Clients[m.client]
	if it.Kind == data.KindPullRequest {
		if dir, ok := client.RepoDir(it.Repo); ok {
			return dir, true
		}
	}
	if pr := m.linkedPR(it.Key); pr != nil {
		if dir, ok := client.RepoDir(pr.Repo); ok {
			return dir, true
		}
	}
	if brs := m.localBranches(it.Key); len(brs) > 0 {
		return brs[0].RepoDir, true
	}
	// Family inheritance: a story with no link of its own borrows from its
	// subtasks' PRs/branches (per-repo subtask stories are common).
	for _, st := range it.Subtasks {
		if pr := m.linkedPR(st.Key); pr != nil {
			if dir, ok := client.RepoDir(pr.Repo); ok {
				return dir, true
			}
		}
		if brs := m.localBranches(st.Key); len(brs) > 0 {
			return brs[0].RepoDir, true
		}
	}
	if proj, _, ok := strings.Cut(it.Key, "-"); ok {
		if repo := client.RepoMap[proj]; repo != "" {
			if dir, exists := client.RepoDir(repo); exists {
				return dir, true
			}
		}
	}
	return client.ProjectsBase(), false
}

// chatContext serializes what standup knows about the item for the system
// prompt of the first turn.
func (m *Model) chatContext(it data.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are chatting inside a terminal dashboard about %s: %s (%s). ", it.Key, it.Title, it.URL)
	fmt.Fprintf(&b, "Status: %s. ", it.Status)
	if it.Description != "" {
		fmt.Fprintf(&b, "\n\nDescription:\n%s\n", it.Description)
	}
	if prs := m.linkedPRs(it.Key); len(prs) > 0 {
		b.WriteString("\nLinked PRs:\n")
		for _, p := range prs {
			state := "open, CI " + strings.ToLower(orDash(p.CIState))
			if p.Merged {
				state = "merged, post-merge CI " + strings.ToLower(orDash(p.MergeCIState))
			}
			fmt.Fprintf(&b, "- %s (%s) branch %s — %s\n", p.Key, state, p.Branch, p.URL)
		}
	}
	b.WriteString("\nKeep answers concise (terminal chat). When asked to comment on Jira or change code, use the tools/skills available to you and say what you did.")
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// handleDock handles keys while the chat dock is focused.
func (m Model) handleDock(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cs := m.chats[m.dock.key]
	switch msg.String() {
	case "esc", "ctrl+x":
		// Close the dock. A running turn keeps going in the background —
		// the footer pings when it finishes, A reopens the conversation.
		m.chatInput.Blur()
		m.dock = nil
		return m, nil
	case "ctrl+c":
		if cs != nil && cs.running {
			cs.cancel()
			cs.running = false
			cs.msgs = append(cs.msgs, chatMsg{role: chatRoleSystem, text: "· cancelled"})
			return m, nil
		}
		return m, tea.Quit
	case "ctrl+o":
		// Open the full transcript in $EDITOR: complete history as plain
		// text — select, search, and copy anything, then quit to return.
		path, err := writeTranscript(cs)
		if err != nil {
			m.status = "transcript: " + err.Error()
			return m, nil
		}
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = os.Getenv("VISUAL")
		}
		if editor == "" {
			editor = "vim"
		}
		c := exec.Command(editor, path)
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			return execDoneMsg{name: "transcript", err: err}
		})
	case "ctrl+y":
		for i := len(cs.msgs) - 1; i >= 0; i-- {
			if cs.msgs[i].role == chatRoleAssistant {
				m.copyURL(cs.msgs[i].text, "last reply")
				return m, nil
			}
		}
		m.status = "no reply to copy yet"
		return m, nil
	case "ctrl+u", "pgup":
		cs.scroll += 5
		return m, nil
	case "ctrl+d", "pgdown":
		if cs.scroll -= 5; cs.scroll < 0 {
			cs.scroll = 0
		}
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.chatInput.Value())
		if text == "" || cs == nil || cs.running {
			return m, nil
		}
		system := ""
		if cs.claudeID == "" {
			system = m.chatContext(cs.item)
		}
		client := m.cfg.Clients[m.client]
		stream, err := chat.Send(cs.repoDir, cs.claudeID, system, m.cfg.ChatPermissionMode(), text,
			client.EnvList(), m.cfg.ChatTools(&client))
		if err != nil {
			m.status = "chat: " + err.Error()
			return m, nil
		}
		m.chatInput.Reset()
		cs.msgs = append(cs.msgs, chatMsg{role: chatRoleUser, text: text})
		cs.events = stream.Events
		cs.cancel = stream.Cancel
		cs.running = true
		cs.scroll = 0
		return m, m.listenChat(cs)
	}
	var cmd tea.Cmd
	m.chatInput, cmd = m.chatInput.Update(msg)
	return m, cmd
}

func (m Model) listenChat(cs *chatSession) tea.Cmd {
	events := cs.events
	key := cs.itemKey
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return chatEvMsg{key: key, ev: chat.Event{Done: true}}
		}
		return chatEvMsg{key: key, ev: ev}
	}
}

func (m Model) applyChatEvent(msg chatEvMsg) (tea.Model, tea.Cmd) {
	cs := m.chats[msg.key]
	if cs == nil {
		return m, nil
	}
	ev := msg.ev
	switch {
	case ev.Tool != "":
		cs.msgs = append(cs.msgs, chatMsg{role: chatRoleTool, text: ev.Tool})
	case ev.Delta != "":
		if n := len(cs.msgs); n > 0 && cs.msgs[n-1].role == chatRoleAssistant {
			cs.msgs[n-1].text += "\n\n" + ev.Delta
		} else {
			cs.msgs = append(cs.msgs, chatMsg{role: chatRoleAssistant, text: ev.Delta})
		}
	}
	// Follow the tail only when the user hasn't scrolled up.
	if ev.Done {
		cs.running = false
		if ev.SessionID != "" {
			cs.claudeID = ev.SessionID
		}
		if ev.Err != nil {
			cs.msgs = append(cs.msgs, chatMsg{role: chatRoleSystem, text: "⚠ " + ev.Err.Error()})
		}
		if m.dock == nil || m.dock.key != cs.itemKey {
			m.status = "✓ chat " + cs.itemKey + " replied — A to view"
		}
		return m, nil
	}
	return m, m.listenChat(cs)
}

// viewDock renders the bottom chat panel at exactly dockHeight lines.
func (m Model) viewDock() string {
	cs := m.chats[m.dock.key]
	h := m.dockHeight()
	w := m.width - 2
	if w < 40 {
		w = 40
	}
	wrap := lipgloss.NewStyle().Width(w)

	headStyle := chatClaudeStyle
	hint := "enter send · esc close · ctrl+y copy reply · ctrl+o full transcript · ctrl+u/d scroll"
	if cs.running {
		hint = "ctrl+c cancel · " + hint + " (turn keeps running when closed)"
	}
	if m.status != "" {
		hint = m.status
	}
	label := " chat · " + m.dock.key + " · " + cs.repoDir + " "
	bar := label + strings.Repeat("─", maxInt(0, w-lipgloss.Width(label)))
	header := headStyle.Render(bar)

	var lines []string
	push := func(str string) {
		lines = append(lines, strings.Split(wrap.Render(str), "\n")...)
	}
	for _, msg := range cs.msgs {
		switch msg.role {
		case chatRoleUser:
			push(chatYouStyle.Render("you ▸ ") + msg.text)
		case chatRoleAssistant:
			push(chatClaudeStyle.Render("claude ▸ ") + jirafmt.Markdown(msg.text))
		case chatRoleTool:
			if strings.HasPrefix(msg.text, "✗") {
				push(errStyle.Render("  " + msg.text))
			} else {
				push(chatToolStyle.Render("  ⚒ " + msg.text))
			}
		default:
			push(subtaskStyle.Render(msg.text))
		}
	}
	if cs.running {
		push(m.spin.View() + subtaskStyle.Render(" thinking…"))
	}

	m.chatInput.SetWidth(w)
	input := m.chatInput.View()
	footer := input + "\n" + helpStyle.MaxWidth(m.width).Render(hint)

	avail := h - 1 - lipgloss.Height(footer)
	if avail < 1 {
		avail = 1
	}
	start := len(lines) - avail - cs.scroll
	if start < 0 {
		start = 0
	}
	end := start + avail
	if end > len(lines) {
		end = len(lines)
	}
	body := padToHeight(strings.Join(lines[start:end], "\n"), avail)
	return lipgloss.NewStyle().Padding(0, 1).Render(header + "\n" + body + "\n" + footer)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// writeTranscript dumps the conversation as plain markdown to a state file.
func writeTranscript(cs *chatSession) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".local", "state", "standup", "transcripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# chat · %s · %s\n\n", cs.itemKey, cs.repoDir)
	for _, msg := range cs.msgs {
		switch msg.role {
		case chatRoleUser:
			b.WriteString("## you\n\n" + msg.text + "\n\n")
		case chatRoleAssistant:
			b.WriteString("## claude\n\n" + msg.text + "\n\n")
		case chatRoleTool:
			b.WriteString("> ⚒ " + msg.text + "\n\n")
		default:
			b.WriteString("> " + msg.text + "\n\n")
		}
	}
	path := filepath.Join(dir, cs.itemKey+".md")
	return path, os.WriteFile(path, []byte(b.String()), 0o600)
}
