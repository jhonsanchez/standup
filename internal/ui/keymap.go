package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jhonsanchez/standup/internal/data"
)

type keyCtx int

const (
	ctxList keyCtx = 1 << iota
	ctxDetail
)

// binding is the single source of truth for one action: its key(s), a full
// description for the `?` menu, a short word for the footer, and when it
// applies. Key dispatch, the footer, and the shortcuts menu all read from it.
type binding struct {
	id      string   // stable id, used for config remapping (keys: {id: "x"})
	primary string   // the remappable key (tea msg.String() form)
	aliases []string // fixed extra keys (arrows, etc.)
	label   string   // display label; defaults to primary
	short   string   // footer word(s)
	desc    string   // full sentence for the shortcuts menu
	group   string
	ctx     keyCtx
	when    func(*Model) bool // nil = always available in its context
	fixed   bool              // not remappable
}

func (b *binding) keyLabel() string {
	if b.label != "" {
		return b.label
	}
	return b.primary
}

type keymap struct {
	order []*binding
	byID  map[string]*binding
}

// Groups in menu order.
const (
	grpNavigate = "Navigate"
	grpViews    = "Views"
	grpItem     = "Item"
	grpGit      = "Git & tools"
	grpCopy     = "Copy & open"
	grpSystem   = "System"
)

var groupOrder = []string{grpNavigate, grpViews, grpItem, grpGit, grpCopy, grpSystem}

func defaultKeymap() *keymap {
	bs := []*binding{
		// Navigate
		{id: "down", primary: "j", aliases: []string{"down"}, group: grpNavigate,
			short: "j/k move", desc: "Move down (scrolls in detail views)", ctx: ctxList | ctxDetail},
		{id: "up", primary: "k", aliases: []string{"up"}, group: grpNavigate,
			desc: "Move up", ctx: ctxList | ctxDetail},
		{id: "page", fixed: true, label: "^d/^u  ^f/^b", group: grpNavigate,
			aliases: []string{"ctrl+d", "ctrl+u", "ctrl+f", "ctrl+b", "pgdown", "pgup"},
			desc:    "Scroll half a page (^d/^u) or a full page (^f/^b)", ctx: ctxList | ctxDetail},
		{id: "top", primary: "home", label: "home / gg", group: grpNavigate,
			desc: "Jump to the top (gg works in detail views)", ctx: ctxList | ctxDetail},
		{id: "bottom", primary: "G", aliases: []string{"end"}, group: grpNavigate,
			desc: "Jump to the bottom", ctx: ctxList | ctxDetail},

		// Views
		{id: "view-toggle", primary: "tab", aliases: []string{"shift+tab"}, group: grpViews,
			desc: "Switch between the Issues and Pull Requests views", ctx: ctxList},
		{id: "view-issues", primary: "i", group: grpViews,
			desc: "Go to the Issues view", ctx: ctxList},
		{id: "view-prs", primary: "p", group: grpViews,
			desc: "Go to the Pull Requests view", ctx: ctxList},
		{id: "clients", fixed: true, label: "1-9", group: grpViews,
			desc: "Switch client directly by number", ctx: ctxList,
			when: func(m *Model) bool { return len(m.cfg.Clients) > 1 }},
		{id: "client-picker", primary: "w", group: grpViews,
			desc: "Open the client switcher (reveals client names)", ctx: ctxList,
			when: func(m *Model) bool { return len(m.cfg.Clients) > 1 }},

		// Item
		{id: "detail", primary: "enter", group: grpItem, short: "enter detail",
			desc: "Open the detail view (headers: toggle the group)", ctx: ctxList},
		{id: "detail-alt", primary: "d", group: grpItem,
			desc: "Open the detail view (alias of enter)", ctx: ctxList},
		{id: "expand", primary: "l", aliases: []string{"right"}, label: "→", group: grpItem,
			desc: "Expand subtasks or open a PR group", ctx: ctxList},
		{id: "collapse", primary: "h", aliases: []string{"left"}, label: "←", group: grpItem,
			desc: "Collapse; on a child row, jump back to its parent", ctx: ctxList},
		{id: "toggle", primary: " ", label: "space", group: grpItem,
			desc: "Toggle subtasks or the PR group under the cursor", ctx: ctxList},
		{id: "groups-all", primary: "z", group: grpItem,
			desc: "Collapse or expand all PR groups at once", ctx: ctxList,
			when: func(m *Model) bool { return m.view == viewPRs }},

		// Git & tools
		{id: "git-view", primary: "g", group: grpGit, short: "g git",
			desc: "Open the linked pull request's detail (git view)", ctx: ctxList,
			when: (*Model).cursorHasGit},
		{id: "jump", primary: "p", group: grpGit, short: "p link",
			desc: "Jump between a Jira issue and its linked PR(s)", ctx: ctxDetail,
			when: func(m *Model) bool { return len(m.jumpTargets()) > 0 }},
		{id: "checkout", primary: "c", group: grpGit,
			desc: "Checkout the item's branch in the local clone (fetch + switch)", ctx: ctxDetail,
			when: (*Model).detailHasClone},
		{id: "git-ui", primary: "L", group: grpGit,
			desc: "Open the git TUI (lazygit) in the item's repo", ctx: ctxDetail,
			when: (*Model).detailHasClone},
		{id: "terminal", primary: "t", group: grpGit,
			desc: "Open a shell in the item's repo", ctx: ctxDetail,
			when: (*Model).detailHasClone},
		{id: "agent", primary: "a", group: grpGit,
			desc: "Launch the AI agent in the repo, pre-prompted with this item", ctx: ctxDetail,
			when: (*Model).detailHasClone},

		// Copy & open
		{id: "copy", primary: "y", group: grpCopy, short: "y copy",
			desc: "Copy the selected item's link", ctx: ctxList | ctxDetail},
		{id: "copy-linked", primary: "Y", group: grpCopy,
			desc: "Copy the counterpart's link (issue⇄PR)", ctx: ctxList | ctxDetail,
			when: (*Model).hasCounterpart},
		{id: "open", primary: "o", group: grpCopy, short: "o browser",
			desc: "Open the selected item in the browser", ctx: ctxList | ctxDetail},

		// System
		{id: "refresh", primary: "r", group: grpSystem,
			desc: "Refresh the current client now", ctx: ctxList},
		{id: "filter", primary: "/", group: grpSystem,
			desc: "Filter items by key, title, or status (esc clears)", ctx: ctxList},
		{id: "edit-config", primary: "e", group: grpSystem,
			desc: "Edit the config in $EDITOR — hot-reloads on save", ctx: ctxList},
		{id: "back", primary: "esc", aliases: []string{"backspace"}, group: grpSystem, short: "esc back",
			desc: "Go back one detail level", ctx: ctxDetail},
		{id: "to-list", primary: "q", group: grpSystem,
			desc: "Close details and return to the list", ctx: ctxDetail},
		{id: "help", primary: "?", group: grpSystem, short: "? help",
			desc: "Show this shortcuts menu", ctx: ctxList | ctxDetail},
		{id: "quit", primary: "q", aliases: []string{"ctrl+c"}, group: grpSystem,
			desc: "Quit standup", ctx: ctxList},
	}
	km := &keymap{order: bs, byID: map[string]*binding{}}
	for _, b := range bs {
		km.byID[b.id] = b
	}
	return km
}

// Is reports whether the key message matches the binding with the given id.
func (k *keymap) Is(msg tea.KeyMsg, id string) bool {
	b := k.byID[id]
	if b == nil {
		return false
	}
	s := msg.String()
	if s == b.primary && b.primary != "" {
		return true
	}
	for _, a := range b.aliases {
		if s == a {
			return true
		}
	}
	return false
}

func (k *keymap) label(id string) string {
	if b := k.byID[id]; b != nil {
		return b.keyLabel()
	}
	return ""
}

func (k *keymap) short(id string) string {
	b := k.byID[id]
	if b == nil {
		return ""
	}
	if b.short != "" {
		return b.short
	}
	return b.keyLabel() + " " + b.id
}

// applyOverrides remaps primary keys from the config (keys: {id: "x"}) and
// returns human-readable warnings for unknown ids or conflicts.
func (k *keymap) applyOverrides(overrides map[string]string) []string {
	var warns []string
	for id, key := range overrides {
		b, ok := k.byID[id]
		if !ok {
			warns = append(warns, fmt.Sprintf("keys: unknown action %q", id))
			continue
		}
		if b.fixed {
			warns = append(warns, fmt.Sprintf("keys: %q is not remappable", id))
			continue
		}
		b.primary = key
		b.label = key
	}
	// Conflict detection within each context.
	for _, ctx := range []keyCtx{ctxList, ctxDetail} {
		seen := map[string]string{}
		for _, b := range k.order {
			if b.ctx&ctx == 0 || b.primary == "" {
				continue
			}
			if prev, dup := seen[b.primary]; dup {
				warns = append(warns, fmt.Sprintf("keys: %q and %q both bound to %q", prev, b.id, b.primary))
			}
			seen[b.primary] = b.id
		}
	}
	return warns
}

// ---- availability helpers ----

// cursorInfo returns the item under the cursor and, for subtask rows, the
// subtask key.
func (m *Model) cursorInfo() (it *data.Item, key string, ok bool) {
	rows := m.rows()
	if len(rows) == 0 {
		return nil, "", false
	}
	cur := m.cursor[m.key()]
	if cur >= len(rows) {
		cur = len(rows) - 1
	}
	r := rows[cur]
	if r.kind == rowHeader {
		return nil, "", false
	}
	items := m.items()
	item := items[r.item]
	key = item.Key
	if r.kind == rowSubtask {
		key = item.Subtasks[r.subtask].Key
	}
	return &item, key, true
}

func (m *Model) cursorHasGit() bool {
	it, key, ok := m.cursorInfo()
	if !ok {
		return false
	}
	return it.Kind == data.KindPullRequest || m.linkedPR(key) != nil
}

func (m *Model) hasCounterpart() bool {
	if len(m.detailStack) > 0 {
		return len(m.jumpTargets()) > 0
	}
	it, key, ok := m.cursorInfo()
	if !ok {
		return false
	}
	return m.counterpart(*it, key) != nil
}

func (m *Model) detailHasClone() bool {
	repo, _, ok := m.detailTarget()
	if !ok {
		return false
	}
	_, exists := m.cfg.Clients[m.client].RepoDir(repo)
	return exists
}
