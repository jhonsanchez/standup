package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
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

// openChat pushes the chat view for an item onto the detail stack.
func (m *Model) openChat(it data.Item) (tea.Model, tea.Cmd) {
	cs := m.chats[it.Key]
	if cs == nil {
		cs = &chatSession{itemKey: it.Key, repoDir: m.chatRepoDir(it)}
		cs.msgs = append(cs.msgs, chatMsg{role: chatRoleSystem, text: fmt.Sprintf(
			"chat about %s · claude runs in %s — ask questions, or tell it to comment on Jira or fix the code (your skills/permissions apply)",
			it.Key, cs.repoDir)})
		m.chats[it.Key] = cs
	}
	st := detailState{item: it, isChat: true}
	st.item.URL = it.URL + "#chat"
	m.pick = nil
	m.status = ""
	m.detailStack = append(m.detailStack, st)
	m.chatInput.Reset()
	m.chatInput.Focus()
	return *m, textarea.Blink
}

// chatRepoDir picks where claude runs: the linked PR's clone, a matching
// branch's clone, or the client's projects dir as a fallback.
func (m *Model) chatRepoDir(it data.Item) string {
	client := m.cfg.Clients[m.client]
	if it.Kind == data.KindPullRequest {
		if dir, ok := client.RepoDir(it.Repo); ok {
			return dir
		}
	}
	if pr := m.linkedPR(it.Key); pr != nil {
		if dir, ok := client.RepoDir(pr.Repo); ok {
			return dir
		}
	}
	if brs := m.localBranches(it.Key); len(brs) > 0 {
		return brs[0].RepoDir
	}
	return client.ProjectsBase()
}

// chatContext serializes what standup knows about the item for the system
// prompt of the first turn.
func (m *Model) chatContext(t *detailState) string {
	it := t.item
	var b strings.Builder
	fmt.Fprintf(&b, "You are chatting inside a terminal dashboard about %s: %s (%s). ", it.Key, it.Title, strings.TrimSuffix(it.URL, "#chat"))
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

// updateChat handles keys while the chat view is on top.
func (m Model) updateChat(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	t := m.top()
	cs := m.chats[t.item.Key]
	switch msg.String() {
	case "esc":
		if cs != nil && cs.running {
			cs.cancel()
			cs.running = false
			cs.msgs = append(cs.msgs, chatMsg{role: chatRoleSystem, text: "· cancelled"})
			return m, nil
		}
		m.chatInput.Blur()
		m.detailStack = m.detailStack[:len(m.detailStack)-1]
		return m, nil
	case "ctrl+c":
		if cs != nil && cs.running {
			cs.cancel()
			cs.running = false
			return m, nil
		}
		return m, tea.Quit
	case "ctrl+u", "pgup":
		cs.scroll += 10
		return m, nil
	case "ctrl+d", "pgdown":
		if cs.scroll -= 10; cs.scroll < 0 {
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
			system = m.chatContext(t)
		}
		stream, err := chat.Send(cs.repoDir, cs.claudeID, system, m.cfg.ChatPermissionMode(), text)
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
	if ev.Done {
		cs.running = false
		if ev.SessionID != "" {
			cs.claudeID = ev.SessionID
		}
		if ev.Err != nil {
			cs.msgs = append(cs.msgs, chatMsg{role: chatRoleSystem, text: "⚠ " + ev.Err.Error()})
		}
		return m, nil
	}
	cs.scroll = 0
	return m, m.listenChat(cs)
}

// viewChat renders the conversation with the input pinned at the bottom.
func (m Model) viewChat(t *detailState) string {
	cs := m.chats[t.item.Key]
	w := m.width - 4
	if w < 40 {
		w = 40
	}
	wrap := lipgloss.NewStyle().Width(w)

	pinned := []string{
		lipgloss.NewStyle().MaxWidth(w).Render(
			hyperlink(strings.TrimSuffix(t.item.URL, "#chat"), headerLabel.Render(t.item.Key)) +
				" — chat " + subtaskStyle.Render("("+cs.repoDir+")")),
		subtaskStyle.Render(strings.Repeat("─", w)),
	}

	var lines []string
	push := func(s string) {
		lines = append(lines, strings.Split(wrap.Render(s), "\n")...)
	}
	for _, msg := range cs.msgs {
		switch msg.role {
		case chatRoleUser:
			push(chatYouStyle.Render("you ▸ ") + msg.text)
		case chatRoleAssistant:
			push(chatClaudeStyle.Render("claude ▸"))
			push(jirafmt.Markdown(msg.text))
		case chatRoleTool:
			push(chatToolStyle.Render("  ⚒ " + msg.text))
		default:
			push(subtaskStyle.Render(msg.text))
		}
		push("")
	}
	if cs.running {
		push(m.spin.View() + subtaskStyle.Render(" thinking… (esc cancels)"))
	}

	m.chatInput.SetWidth(w)
	inputView := m.chatInput.View()
	help := "enter send · esc back"
	if cs.running {
		help = "esc cancel · " + help
	}
	footer := inputView + "\n" + helpStyle.MaxWidth(m.width).Render(help)

	avail := m.termHeight() - len(pinned) - lipgloss.Height(footer) - 1
	if avail < 3 {
		avail = 3
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
	return lipgloss.NewStyle().Padding(0, 1).Render(
		strings.Join(pinned, "\n") + "\n" + body + "\n" + footer)
}
