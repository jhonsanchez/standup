package ui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/atotto/clipboard"
	"github.com/aymanbagabas/go-osc52/v2"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jhonsanchez/standup/internal/config"
	"github.com/jhonsanchez/standup/internal/data"
	"github.com/jhonsanchez/standup/internal/github"
	"github.com/jhonsanchez/standup/internal/gitscan"
	"github.com/jhonsanchez/standup/internal/jira"
	"github.com/jhonsanchez/standup/internal/upgrade"
)

type view int

const (
	viewIssues view = iota
	viewPRs
)

var openCommand = func() string {
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}()

type clientState struct {
	loaded    bool
	loading   bool
	fetchedAt time.Time
	prs       []data.Item
	merged    []data.Item // recently merged, for issue linking + post-merge CI
	issues    []data.Item
	branches  []data.BranchRef
	errs      []string
}

type fetchedMsg struct {
	client   int
	prs      []data.Item
	merged   []data.Item
	issues   []data.Item
	branches []data.BranchRef
	errs     []string
}

type rowKind int

const (
	rowItem rowKind = iota
	rowSubtask
	rowHeader
)

// row is one visible line: a group header, an item, or one of its subtasks.
type row struct {
	kind    rowKind
	item    int
	subtask int // -1 for the item line itself
	bucket  data.Bucket
	count   int
}

type Model struct {
	cfg       *config.Config
	states    []clientState
	client    int
	view      view
	cursor    map[string]int // per client+view
	expand    map[string]bool
	collapsed map[string]bool // PR bucket collapse state
	spin      spinner.Model
	filter    textinput.Model
	filterOn  bool
	width     int
	height    int
	status    string

	version        string // running version, for the update notice
	updateAvail    string // newer release tag, when detected
	updateApplied  string // tag auto-installed in the background
	detailStack    []detailState
	pick           []data.Item // jump-target picker overlay
	pendingG       bool        // first g of a gg sequence (detail view)
	clientPick     bool        // client-switcher overlay (list view)
	showHelp       bool        // `?` shortcuts menu
	km             *keymap
	chats          map[string]*chatSession
	chatInput      textarea.Model
	chatRepoChoice map[string]string // per-issue repo pick for the chat
	repoPick       *repoPickState    // chat repo chooser overlay
}

func New(cfg *config.Config, version string) Model {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = lipgloss.NewStyle().Foreground(colPurple)
	fi := textinput.New()
	fi.Placeholder = "filter…"
	fi.Prompt = "/ "
	fi.CharLimit = 80
	setIcons(cfg.Icons)
	if cfg.LinkPattern != "" {
		if re, err := regexp.Compile(cfg.LinkPattern); err == nil {
			issueKeyRe = re
		}
	}
	ta := textarea.New()
	ta.Placeholder = "ask, or tell claude what to do…"
	ta.SetHeight(2)
	ta.ShowLineNumbers = false
	km := defaultKeymap()
	warns := km.applyOverrides(cfg.Keys)
	m := Model{
		cfg:            cfg,
		states:         make([]clientState, len(cfg.Clients)),
		cursor:         map[string]int{},
		expand:         map[string]bool{},
		collapsed:      map[string]bool{},
		spin:           sp,
		filter:         fi,
		km:             km,
		version:        version,
		chats:          map[string]*chatSession{},
		chatInput:      ta,
		chatRepoChoice: map[string]string{},
	}
	m.chatInput = ta
	if len(warns) > 0 {
		m.status = "⚠ " + strings.Join(warns, " · ")
	}
	if cfg.Icons != "ascii" && !NerdFontDetected() {
		m.status = "⚠ no Nerd Font detected — icons may look broken; install JetBrainsMono Nerd Font or set `icons: ascii`"
	}
	return m
}

// nerdFontDetected makes a best-effort check for an installed Nerd Font.
// Terminals don't expose their font, so this only inspects the system's
// installed font files.
func NerdFontDetected() bool {
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		for _, dir := range []string{filepath.Join(home, "Library", "Fonts"), "/Library/Fonts"} {
			matches, _ := filepath.Glob(filepath.Join(dir, "*Nerd*"))
			if len(matches) > 0 {
				return true
			}
		}
		return false
	}
	out, err := exec.Command("fc-list").Output()
	if err != nil {
		return true // can't tell — don't nag
	}
	return strings.Contains(strings.ToLower(string(out)), "nerd")
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.fetch(m.client), m.autoTick(), m.checkForUpdate())
}

type newVersionMsg struct {
	tag string
}

type autoUpdatedMsg struct {
	tag string
	err error
}

// checkForUpdate resolves the latest release tag once at startup (via the
// release redirect — no API, no rate limits) and surfaces a footer notice.
func (m Model) checkForUpdate() tea.Cmd {
	if m.version == "" || m.version == "dev" {
		return nil
	}
	return func() tea.Msg {
		client := &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := client.Head("https://github.com/jhonsanchez/standup/releases/latest")
		if err != nil {
			return newVersionMsg{}
		}
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		if i := strings.LastIndex(loc, "/tag/"); i >= 0 {
			return newVersionMsg{tag: loc[i+len("/tag/"):]}
		}
		return newVersionMsg{}
	}
}

type autoTickMsg struct{}

// autoTick schedules the next background refresh of the active client.
func (m Model) autoTick() tea.Cmd {
	d := m.cfg.RefreshInterval()
	if d <= 0 {
		return nil
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return autoTickMsg{} })
}

func (m Model) fetch(idx int) tea.Cmd {
	c := m.cfg.Clients[idx]
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		msg := fetchedMsg{client: idx}
		var mu sync.Mutex
		var wg sync.WaitGroup

		if c.Jira != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				items, err := jira.FetchSprintIssues(ctx, c.Jira)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					msg.errs = append(msg.errs, "jira: "+err.Error())
					return
				}
				msg.issues = append(items, msg.issues...)
			}()
		}
		if c.GitHub != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				prs, merged, issues, err := github.Fetch(ctx, c.GitHub)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					msg.errs = append(msg.errs, "github: "+err.Error())
					return
				}
				msg.prs = prs
				msg.merged = merged
				msg.issues = append(msg.issues, issues...)
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			branches := gitscan.Scan(c.ProjectsBase())
			mu.Lock()
			defer mu.Unlock()
			msg.branches = branches
		}()
		wg.Wait()
		return msg
	}
}

type configEditedMsg struct {
	err error
}

// reloadConfig re-reads the config after `e`. A broken file keeps the old
// config running and surfaces the error instead of crashing the session.
func (m Model) reloadConfig(msg configEditedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = "editor: " + msg.err.Error()
		return m, nil
	}
	newCfg, err := config.Load()
	if err != nil {
		m.status = "⚠ config not reloaded: " + err.Error()
		return m, nil
	}
	// Preserve single-client mode (`standup <client>`).
	if len(m.cfg.Clients) == 1 && len(newCfg.Clients) > 1 {
		name := m.cfg.Clients[0].Name
		for _, c := range newCfg.Clients {
			if c.Name == name {
				newCfg.Clients = []config.Client{c}
				break
			}
		}
	}
	setIcons(newCfg.Icons)
	issueKeyRe = defaultIssueKeyRe
	if newCfg.LinkPattern != "" {
		if re, err := regexp.Compile(newCfg.LinkPattern); err == nil {
			issueKeyRe = re
		}
	}
	m.km = defaultKeymap()
	if warns := m.km.applyOverrides(newCfg.Keys); len(warns) > 0 {
		m.status = "⚠ " + strings.Join(warns, " · ")
	}
	m.cfg = newCfg
	m.states = make([]clientState, len(newCfg.Clients))
	if m.client >= len(newCfg.Clients) {
		m.client = 0
	}
	m.cursor = map[string]int{}
	m.expand = map[string]bool{}
	m.collapsed = map[string]bool{}
	m.detailStack = nil
	m.states[m.client].loading = true
	m.status = "✓ config reloaded"
	return m, m.fetch(m.client)
}

func (m *Model) key() string {
	return fmt.Sprintf("%d/%d", m.client, m.view)
}

func (m *Model) items() []data.Item {
	st := m.states[m.client]
	var src []data.Item
	if m.view == viewPRs {
		src = st.prs
	} else {
		src = st.issues
	}
	// A subtask assigned to you is also returned by the sprint query as a
	// top-level issue. When its parent is in the list too, show it only
	// nested under the parent — not duplicated at the top level.
	if m.view == viewIssues {
		subKeys := map[string]bool{}
		for _, it := range src {
			if it.Kind == data.KindJiraIssue {
				for _, st := range it.Subtasks {
					subKeys[st.Key] = true
				}
			}
		}
		if len(subKeys) > 0 {
			var kept []data.Item
			for _, it := range src {
				if !subKeys[it.Key] {
					kept = append(kept, it)
				}
			}
			src = kept
		}
	}

	f := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if f == "" {
		return src
	}
	var out []data.Item
	for _, it := range src {
		if strings.Contains(strings.ToLower(it.Title), f) ||
			strings.Contains(strings.ToLower(it.Key), f) ||
			strings.Contains(strings.ToLower(it.Status), f) {
			out = append(out, it)
		}
	}
	return out
}

func (m *Model) bucketKey(b data.Bucket) string {
	return fmt.Sprintf("bucket:%d:%d", m.client, b)
}

func (m *Model) rows() []row {
	items := m.items()
	var rows []row

	if m.view == viewPRs {
		byBucket := map[data.Bucket][]int{}
		for i, it := range items {
			b := it.Bucket
			if b == data.BucketNone {
				b = data.BucketOther
			}
			byBucket[b] = append(byBucket[b], i)
		}
		for _, b := range data.BucketOrder {
			idxs := byBucket[b]
			if len(idxs) == 0 {
				continue
			}
			rows = append(rows, row{kind: rowHeader, item: -1, subtask: -1, bucket: b, count: len(idxs)})
			if !m.collapsed[m.bucketKey(b)] {
				for _, i := range idxs {
					rows = append(rows, row{kind: rowItem, item: i, subtask: -1, bucket: b})
				}
			}
		}
		return rows
	}

	for i, it := range items {
		rows = append(rows, row{kind: rowItem, item: i, subtask: -1})
		if it.Kind == data.KindJiraIssue && len(it.Subtasks) > 0 && m.expand[it.Key] {
			for s := range it.Subtasks {
				rows = append(rows, row{kind: rowSubtask, item: i, subtask: s})
			}
		}
	}
	return rows
}

func (m *Model) clampCursor(rows []row) int {
	cur := m.cursor[m.key()]
	if cur >= len(rows) {
		cur = len(rows) - 1
	}
	if cur < 0 {
		cur = 0
	}
	m.cursor[m.key()] = cur
	return cur
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case fetchedMsg:
		st := &m.states[msg.client]
		// A refresh that errored must not wipe data already on screen.
		if st.loaded && len(msg.errs) > 0 {
			if len(msg.prs) == 0 {
				msg.prs = st.prs
			}
			if len(msg.issues) == 0 {
				msg.issues = st.issues
			}
		}
		st.loading = false
		st.loaded = true
		st.fetchedAt = time.Now()
		st.prs = msg.prs
		st.merged = msg.merged
		st.issues = msg.issues
		st.branches = msg.branches
		st.errs = msg.errs
		return m, nil

	case newVersionMsg:
		if msg.tag != "" && msg.tag != "v"+m.version {
			m.updateAvail = msg.tag
			if m.cfg.AutoUpdateEnabled() {
				current := m.version
				return m, func() tea.Msg {
					tag, err := upgrade.AutoUpdate(current)
					return autoUpdatedMsg{tag: tag, err: err}
				}
			}
		}
		return m, nil

	case autoUpdatedMsg:
		if msg.err == nil && msg.tag != "" {
			m.updateAvail = ""
			m.updateApplied = msg.tag
		}
		// Errors or skips (brew/go/unwritable) keep the plain notice.
		return m, nil

	case autoTickMsg:
		// Background refresh of the active client only. Skip when a fetch is
		// already running or a manual refresh happened recently, so the
		// servers never see more than one request cycle per interval.
		cmds := []tea.Cmd{m.autoTick()}
		st := &m.states[m.client]
		if st.loaded && !st.loading &&
			time.Since(st.fetchedAt) >= m.cfg.RefreshInterval()*8/10 {
			st.loading = true
			cmds = append(cmds, m.fetch(m.client))
		}
		return m, tea.Batch(cmds...)

	case configEditedMsg:
		return m.reloadConfig(msg)

	case chatEvMsg:
		return m.applyChatEvent(msg)

	case prDetailMsg, jiraDetailMsg, checksMsg, checksTickMsg, checkoutMsg, execDoneMsg:
		return m.updateDetail(msg)

	case tea.KeyMsg:
		if m.repoPick != nil {
			return m.handleRepoPick(msg)
		}
		if m.showHelp {
			// Which-key behavior: esc/? closes; any other key closes AND runs.
			m.showHelp = false
			if msg.String() == "esc" || m.km.Is(msg, "help") {
				return m, nil
			}
		}
		if len(m.detailStack) > 0 {
			return m.updateDetail(msg)
		}
		if m.filterOn {
			switch msg.String() {
			case "enter":
				m.filterOn = false
				m.filter.Blur()
				return m, nil
			case "esc":
				m.filterOn = false
				m.filter.Blur()
				m.filter.SetValue("")
				return m, nil
			default:
				var cmd tea.Cmd
				m.filter, cmd = m.filter.Update(msg)
				return m, cmd
			}
		}
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.rows()
	cur := m.clampCursor(rows)
	m.status = ""

	// Client-switcher overlay: shows names only while open.
	if m.clientPick {
		s := msg.String()
		switch {
		case s == "esc" || s == "w":
			m.clientPick = false
			return m, nil
		case s >= "1" && s <= "9":
			if n := int(s[0] - '1'); n < len(m.cfg.Clients) {
				m.clientPick = false
				m.client = n
				return m.ensureLoaded()
			}
		}
		return m, nil
	}

	switch {
	case m.km.Is(msg, "quit"):
		return m, tea.Quit

	case m.km.Is(msg, "help"):
		m.showHelp = true
		return m, nil

	case m.km.Is(msg, "client-picker"):
		if len(m.cfg.Clients) > 1 {
			m.clientPick = true
		}
		return m, nil

	case m.km.Is(msg, "edit-config"):
		// Edit the config in $EDITOR; hot-reload on return.
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = os.Getenv("VISUAL")
		}
		if editor == "" {
			editor = "vim"
		}
		c := exec.Command(editor, config.Path())
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			return configEditedMsg{err: err}
		})

	case m.km.Is(msg, "view-toggle"):
		m.view = 1 - m.view
		return m, nil
	case msg.String() >= "1" && msg.String() <= "9":
		n := int(msg.String()[0] - '1')
		if n < len(m.cfg.Clients) {
			m.client = n
			return m.ensureLoaded()
		}
		return m, nil

	case m.km.Is(msg, "view-prs"):
		m.view = viewPRs
		return m, nil
	case m.km.Is(msg, "view-issues"):
		m.view = viewIssues
		return m, nil

	case m.km.Is(msg, "expand"):
		// Expand, tree-style.
		if len(rows) == 0 {
			return m, nil
		}
		switch r := rows[cur]; r.kind {
		case rowHeader:
			m.collapsed[m.bucketKey(r.bucket)] = false
		case rowItem:
			it := m.items()[r.item]
			if it.Kind == data.KindJiraIssue && len(it.Subtasks) > 0 {
				m.expand[it.Key] = true
			}
		}
		return m, nil

	case m.km.Is(msg, "collapse"):
		// Collapse; on a child row, jump back to its parent.
		if len(rows) == 0 {
			return m, nil
		}
		switch r := rows[cur]; r.kind {
		case rowHeader:
			m.collapsed[m.bucketKey(r.bucket)] = true
		case rowSubtask:
			parent := m.items()[r.item]
			m.expand[parent.Key] = false
			m.setCursor(func(x row) bool { return x.kind == rowItem && x.item == r.item })
		case rowItem:
			it := m.items()[r.item]
			if it.Kind == data.KindJiraIssue && m.expand[it.Key] {
				m.expand[it.Key] = false
			} else if m.view == viewPRs {
				m.collapsed[m.bucketKey(r.bucket)] = true
				m.setCursor(func(x row) bool { return x.kind == rowHeader && x.bucket == r.bucket })
			}
		}
		return m, nil

	case m.km.Is(msg, "down"):
		if cur < len(rows)-1 {
			m.cursor[m.key()] = cur + 1
		}
		return m, nil
	case m.km.Is(msg, "up"):
		if cur > 0 {
			m.cursor[m.key()] = cur - 1
		}
		return m, nil
	case m.km.Is(msg, "top"):
		m.cursor[m.key()] = 0
		return m, nil
	case m.km.Is(msg, "bottom"):
		m.cursor[m.key()] = len(rows) - 1
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
		next := cur + delta
		if next > len(rows)-1 {
			next = len(rows) - 1
		}
		if next < 0 {
			next = 0
		}
		m.cursor[m.key()] = next
		return m, nil

	case m.km.Is(msg, "copy"), m.km.Is(msg, "copy-linked"):
		// y: copy this row's link · Y: copy the counterpart's (issue⇄PR).
		if len(rows) == 0 || rows[cur].kind == rowHeader {
			return m, nil
		}
		r := rows[cur]
		it := m.items()[r.item]
		key, url := it.Key, it.URL
		if r.kind == rowSubtask {
			s := it.Subtasks[r.subtask]
			key, url = s.Key, s.URL
		}
		if m.km.Is(msg, "copy-linked") {
			if cp := m.counterpart(it, key); cp != nil {
				m.copyURL(cp.URL, cp.Key)
			} else {
				m.status = "no linked item for " + key
			}
		} else {
			m.copyURL(url, key)
		}
		return m, nil

	case m.km.Is(msg, "checks"):
		// GHA checks window straight from the list, via the row's PR.
		it, key, ok := m.cursorInfo()
		if !ok {
			return m, nil
		}
		pr := *it
		if pr.Kind != data.KindPullRequest {
			lp := m.linkedPR(key)
			if lp == nil {
				m.status = "no linked PR for " + key
				return m, nil
			}
			pr = *lp
		}
		return m.openChecks(pr)

	case m.km.Is(msg, "chat"):
		it, _, ok := m.cursorInfo()
		if !ok {
			return m, nil
		}
		return m.openChat(*it)

	case m.km.Is(msg, "git-view"):
		// Git view: jump straight to the linked PR's detail.
		if len(rows) == 0 || rows[cur].kind == rowHeader {
			return m, nil
		}
		r := rows[cur]
		it := m.items()[r.item]
		key := it.Key
		if r.kind == rowSubtask {
			key = it.Subtasks[r.subtask].Key
		}
		if it.Kind == data.KindPullRequest {
			return m.openDetail(it)
		}
		if pr := m.linkedPR(key); pr != nil {
			return m.openDetail(*pr)
		}
		m.status = "no open PR references " + key
		return m, nil

	case m.km.Is(msg, "toggle"):
		// Space toggles: PR groups, Jira subtask expansion.
		if len(rows) == 0 {
			return m, nil
		}
		r := rows[cur]
		if r.kind == rowHeader {
			k := m.bucketKey(r.bucket)
			m.collapsed[k] = !m.collapsed[k]
			return m, nil
		}
		it := m.items()[r.item]
		if it.Kind == data.KindJiraIssue && len(it.Subtasks) > 0 {
			m.expand[it.Key] = !m.expand[it.Key]
		}
		return m, nil

	case m.km.Is(msg, "detail"), m.km.Is(msg, "detail-alt"):
		// Enter always opens detail (headers toggle their group).
		if len(rows) == 0 {
			return m, nil
		}
		r := rows[cur]
		if r.kind == rowHeader {
			k := m.bucketKey(r.bucket)
			m.collapsed[k] = !m.collapsed[k]
			return m, nil
		}
		it := m.items()[r.item]
		if r.kind == rowSubtask {
			s := it.Subtasks[r.subtask]
			return m.openDetail(data.Item{
				Kind:           data.KindJiraIssue,
				Key:            s.Key,
				Title:          s.Summary,
				Status:         s.Status,
				StatusCategory: s.StatusCategory,
				URL:            s.URL,
			})
		}
		return m.openDetail(it)

	case m.km.Is(msg, "groups-all"):
		// Toggle all PR groups at once.
		if m.view == viewPRs {
			any := false
			for _, b := range data.BucketOrder {
				if !m.collapsed[m.bucketKey(b)] {
					any = true
				}
			}
			for _, b := range data.BucketOrder {
				m.collapsed[m.bucketKey(b)] = any
			}
		}
		return m, nil

	case m.km.Is(msg, "open"):
		if len(rows) == 0 || rows[cur].kind == rowHeader {
			return m, nil
		}
		return m, m.open(rows, cur)

	case m.km.Is(msg, "refresh"):
		st := &m.states[m.client]
		if !st.loading {
			st.loading = true
			return m, m.fetch(m.client)
		}
		return m, nil

	case m.km.Is(msg, "filter"):
		m.filterOn = true
		m.filter.Focus()
		return m, textinput.Blink

	case msg.String() == "esc":
		m.filter.SetValue("")
		return m, nil
	}
	return m, nil
}

// setCursor moves the cursor to the first row matching pred.
func (m *Model) setCursor(pred func(row) bool) {
	for i, r := range m.rows() {
		if pred(r) {
			m.cursor[m.key()] = i
			return
		}
	}
}

func (m *Model) ensureLoaded() (tea.Model, tea.Cmd) {
	st := &m.states[m.client]
	if !st.loaded && !st.loading {
		st.loading = true
		return *m, m.fetch(m.client)
	}
	return *m, nil
}

func (m *Model) open(rows []row, cur int) tea.Cmd {
	items := m.items()
	r := rows[cur]
	url := items[r.item].URL
	if r.subtask >= 0 {
		url = items[r.item].Subtasks[r.subtask].URL
	}
	if url == "" {
		return nil
	}
	m.status = "opened " + url
	return openURLCmd(url)
}

func (m Model) termHeight() int {
	if m.height <= 0 {
		return 24
	}
	return m.height
}

// padToHeight clips or pads content to exactly h lines so a footer below it
// always lands on the last terminal rows.
func padToHeight(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) >= h {
		return strings.Join(lines[:h], "\n")
	}
	return s + strings.Repeat("\n", h-len(lines))
}

func (m Model) View() string {
	if m.repoPick != nil {
		return m.viewRepoPick()
	}
	if m.showHelp {
		return m.viewHelp()
	}
	if len(m.detailStack) > 0 {
		return m.viewDetail()
	}
	var b strings.Builder

	// Header: title + client tabs. With hide_clients (or a single client),
	// only the active client renders — other names never appear on screen.
	var tabs []string
	if m.cfg.HideClients || len(m.cfg.Clients) == 1 {
		tabs = append(tabs, clientTabActive.Render(m.cfg.Clients[m.client].Name))
	} else {
		for i, c := range m.cfg.Clients {
			label := fmt.Sprintf("%d:%s", i+1, c.Name)
			if i == m.client {
				tabs = append(tabs, clientTabActive.Render(label))
			} else {
				tabs = append(tabs, clientTabInactive.Render(label))
			}
		}
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Center,
		titleStyle.Render("⏵ standup"), "  ", strings.Join(tabs, " ")))
	b.WriteString("\n\n")

	// View tabs with counts. The count is styled separately — nesting an
	// already-styled string inside the underlined tab style leaks raw ANSI.
	st := m.states[m.client]
	viewTab := func(label string, count int, active bool) string {
		style := viewTabInactive
		if active {
			style = viewTabActive
		}
		return style.Render(label) + countStyle.Render(fmt.Sprint(count)) + " "
	}
	b.WriteString(viewTab("ISSUES", len(st.issues), m.view == viewIssues))
	b.WriteString(viewTab("PULL REQUESTS", len(st.prs), m.view == viewPRs))
	if m.filterOn || m.filter.Value() != "" {
		b.WriteString("   " + m.filter.View())
	}
	b.WriteString("\n\n")
	head := b.String() // 4 rows

	// Pinned footer: errors + status + help.
	var fparts []string
	if m.updateApplied != "" {
		fparts = append(fparts, helpStyle.MaxWidth(m.width).Render(
			"✓ auto-updated to "+m.updateApplied+" — restart standup to apply"))
	} else if m.updateAvail != "" {
		fparts = append(fparts, helpStyle.MaxWidth(m.width).Render(
			"⬆ "+m.updateAvail+" available — run `standup upgrade` (or brew upgrade standup)"))
	}
	if m.clientPick {
		fparts = append(fparts, headerLabel.Render("Switch client:"))
		for i, c := range m.cfg.Clients {
			marker := "  "
			if i == m.client {
				marker = "▸ "
			}
			fparts = append(fparts, fmt.Sprintf("%s%s %s",
				marker, keyStyle.Render(fmt.Sprintf("%d)", i+1)), c.Name))
		}
	}
	for _, e := range st.errs {
		fparts = append(fparts, errStyle.MaxWidth(m.width).Render("⚠ "+e))
	}
	if m.status != "" {
		fparts = append(fparts, helpStyle.MaxWidth(m.width).Render(m.status))
	}
	help := m.listHelp()
	if st.loaded && st.loading {
		help += " · " + m.spin.View() + " refreshing…"
	} else if !st.fetchedAt.IsZero() {
		help += " · updated " + st.fetchedAt.Format("15:04:05")
	}
	fparts = append(fparts, helpStyle.MaxWidth(m.width).Render(help))
	footer := strings.Join(fparts, "\n")

	avail := m.termHeight() - 4 - lipgloss.Height(footer)
	if avail < 3 {
		avail = 3
	}
	body := padToHeight(m.renderBody(st, avail), avail)
	return head + body + "\n" + footer
}

// listHelp is the minimal pinned footer: the highest-value hints for the
// current row plus the permanent `? help`. The full menu lives behind `?`.
func (m Model) listHelp() string {
	if m.clientPick {
		return "1-9 pick · esc cancel"
	}
	var parts []string
	rows := m.rows()
	if len(rows) > 0 {
		cur := m.cursor[m.key()]
		if cur >= len(rows) {
			cur = len(rows) - 1
		}
		switch r := rows[cur]; r.kind {
		case rowHeader:
			if m.collapsed[m.bucketKey(r.bucket)] {
				parts = append(parts, "enter open group")
			} else {
				parts = append(parts, "enter close group")
			}
		default:
			parts = append(parts, m.km.label("detail")+" detail")
			if r.kind == rowItem {
				it := m.items()[r.item]
				if it.Kind == data.KindJiraIssue && len(it.Subtasks) > 0 {
					if m.expand[it.Key] {
						parts = append(parts, "← close")
					} else {
						parts = append(parts, "→ subtasks")
					}
				}
			}
			if (&m).cursorHasGit() {
				parts = append(parts, m.km.label("git-view")+" git", m.km.label("checks")+" checks")
			}
		}
	}
	parts = append(parts, m.km.label("help")+" help")
	return strings.Join(parts, " · ")
}

func (m Model) renderBody(st clientState, avail int) string {
	// Blank body only on the first load — refreshes keep the current list
	// visible (a "refreshing" indicator shows in the footer).
	if !st.loaded && len(st.errs) == 0 {
		return emptyStyle.Render(m.spin.View() + " loading " + m.cfg.Clients[m.client].Name + "…")
	}
	rows := m.rows()
	if len(rows) == 0 {
		if m.filter.Value() != "" {
			return emptyStyle.Render("No items match your current filter.")
		}
		if m.view == viewPRs {
			return emptyStyle.Render("No open pull requests. 🎉")
		}
		return emptyStyle.Render("No issues assigned to you in the current sprint. 🎉")
	}

	cur := m.cursor[fmt.Sprintf("%d/%d", m.client, m.view)]
	if cur >= len(rows) {
		cur = len(rows) - 1
	}
	items := m.items()

	// Viewport: keep cursor visible, reserving a line for the "… more" hint.
	window := avail
	if len(rows) > avail {
		window = avail - 1
	}
	if window < 1 {
		window = 1
	}
	// Keep a few rows of lookahead below the cursor (vim scrolloff) so
	// expanding subtasks/groups at the bottom edge stays visible.
	scrolloff := 3
	if scrolloff > window-1 {
		scrolloff = 0
	}
	start := 0
	if cur >= window-scrolloff {
		start = cur - (window - scrolloff) + 1
	}
	if start+window > len(rows) {
		start = len(rows) - window
	}
	if start < 0 {
		start = 0
	}
	end := start + window
	if end > len(rows) {
		end = len(rows)
	}

	var b strings.Builder
	for ri := start; ri < end; ri++ {
		r := rows[ri]
		var line string
		switch r.kind {
		case rowHeader:
			line = m.renderHeader(r)
		case rowSubtask:
			line = m.renderSubtask(items[r.item].Subtasks[r.subtask])
		default:
			line = m.renderItem(items[r.item])
		}
		if ri == cur {
			line = cursorLineStyle.Render("▍" + line)
		} else {
			line = " " + line
		}
		b.WriteString(line + "\n")
	}
	if end < len(rows) || start > 0 {
		b.WriteString(helpStyle.Render(fmt.Sprintf("… %d more below · %d above", len(rows)-end, start)))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) renderItem(it data.Item) string {
	var parts []string

	switch it.Kind {
	case data.KindJiraIssue:
		marker := " "
		if len(it.Subtasks) > 0 {
			if m.expand[it.Key] {
				marker = "▾"
			} else {
				marker = "▸"
			}
		}
		parts = append(parts, marker, statusBadge(it.StatusCategory, it.Status), hyperlink(it.URL, keyStyle.Render(it.Key)))
		if mark := gitMarker(m.linkedPR(it.Key)); mark != "" {
			parts = append(parts, mark)
		} else if mark := m.branchMarker(it.Key); mark != "" {
			parts = append(parts, mark)
		}
		if len(it.Subtasks) > 0 {
			done := 0
			for _, s := range it.Subtasks {
				if s.StatusCategory == "done" {
					done++
				}
			}
			parts = append(parts, subtaskStyle.Render(fmt.Sprintf("[%d/%d]", done, len(it.Subtasks))))
		}
	case data.KindPullRequest:
		parts = append(parts,
			ageStyle.Render(fmt.Sprintf("%4s", relAge(it.Updated))),
			ciIcon(it.CIState),
			conflictIcon(it.Mergeable),
			hyperlink(it.URL, keyStyle.Render(it.Key)),
			diffStat(it.Additions, it.Deletions))
		if rl := reviewLabel(it.ReviewDecision); rl != "" {
			parts = append(parts, rl)
		}
	case data.KindGHIssue:
		parts = append(parts, " ", badgeOpen.Render("open"), hyperlink(it.URL, keyStyle.Render(it.Key)))
	}

	title := it.Title
	max := m.width - lipgloss.Width(strings.Join(parts, " ")) - 6
	if max > 10 && len(title) > max {
		title = title[:max-1] + "…"
	}
	parts = append(parts, title)
	return strings.Join(parts, " ")
}

// linkedPRs returns fetched PRs for this client that reference the Jira key
// in their title or branch name — open PRs first, then recently merged ones.
func (m Model) linkedPRs(key string) []data.Item {
	var out []data.Item
	st := m.states[m.client]
	for _, p := range st.prs {
		if strings.Contains(p.Title, key) || strings.Contains(p.Branch, key) {
			out = append(out, p)
		}
	}
	for _, p := range st.merged {
		if strings.Contains(p.Title, key) || strings.Contains(p.Branch, key) {
			out = append(out, p)
		}
	}
	return out
}

func (m Model) linkedPR(key string) *data.Item {
	if prs := m.linkedPRs(key); len(prs) > 0 {
		return &prs[0]
	}
	return nil
}

// gitMarker is the compact git status shown next to Jira issues/subtasks
// that have a linked PR: " repo#n" colored by review state + CI + conflicts.
func gitMarker(pr *data.Item) string {
	if pr == nil {
		return ""
	}
	repo := pr.Repo
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		repo = repo[i+1:]
	}
	num := ""
	if i := strings.LastIndex(pr.Key, "#"); i >= 0 {
		num = pr.Key[i:]
	}
	if pr.Merged {
		// Merged: magenta merge glyph + the merge commit's (post-merge) CI.
		return hyperlink(pr.URL, mergedStyle.Render(mergedGlyph+" "+repo+num)) + ciIcon(pr.MergeCIState)
	}
	style := linkStyle
	switch pr.ReviewDecision {
	case "APPROVED":
		style = lipgloss.NewStyle().Foreground(colGreen)
	case "CHANGES_REQUESTED":
		style = lipgloss.NewStyle().Foreground(colOrange)
	}
	return hyperlink(pr.URL, style.Render(githubGlyph+" "+repo+num)) +
		ciIcon(pr.CIState) + strings.TrimRight(conflictIcon(pr.Mergeable), " ")
}

// localBranches returns branches in local clones whose name contains the
// issue key (work started but no PR yet, typically).
func (m Model) localBranches(key string) []data.BranchRef {
	var out []data.BranchRef
	for _, b := range m.states[m.client].branches {
		if strings.Contains(b.Name, key) {
			out = append(out, b)
		}
	}
	return out
}

// branchMarker is the dim marker for issues that have a local/remote branch
// but no open PR: " repo" (nerd-font branch glyph), plus the branch name
// when it isn't just the issue key.
func (m Model) branchMarker(key string) string {
	brs := m.localBranches(key)
	if len(brs) == 0 {
		return ""
	}
	return subtaskStyle.Render(branchGlyph + " " + brs[0].Repo + ":" + brs[0].Name)
}

// counterpart resolves the "other side" of an item: the PR linked to a Jira
// issue/subtask, or the sprint issue referenced by a PR.
func (m Model) counterpart(it data.Item, key string) *data.Item {
	if it.Kind == data.KindPullRequest {
		for _, k := range issueKeyRe.FindAllString(it.Title+" "+it.Branch, -1) {
			for _, is := range m.states[m.client].issues {
				if is.Key == k {
					return &is
				}
			}
		}
		return nil
	}
	return m.linkedPR(key)
}

func (m *Model) copyURL(url, what string) {
	if url == "" {
		m.status = "nothing to copy"
		return
	}
	// OSC52 reaches the *local* clipboard even over SSH/tmux; the system
	// clipboard (pbcopy/xclip) is attempted too as a fallback.
	oscErr := oscCopy(url)
	sysErr := clipboard.WriteAll(url)
	switch {
	case oscErr != nil && sysErr != nil:
		m.status = "copy failed — needs an OSC52-capable terminal, or xclip/xsel on Linux"
	case sysErr != nil:
		// Only OSC52 was emitted; the terminal must honor it (iTerm2:
		// Settings→General→Selection→clipboard access; tmux: set-clipboard on).
		m.status = "copied " + what + " via OSC52 — if paste is stale, enable clipboard access in your terminal"
	default:
		m.status = "copied " + what + " → " + url
	}
}

// oscCopy writes the text to the terminal's clipboard via OSC52.
func oscCopy(text string) error {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer tty.Close()
	seq := osc52.New(text)
	if os.Getenv("TMUX") != "" {
		seq = seq.Tmux()
	}
	_, err = seq.WriteTo(tty)
	return err
}

func (m Model) renderHeader(r row) string {
	marker := "▾"
	if m.collapsed[m.bucketKey(r.bucket)] {
		marker = "▸"
	}
	return fmt.Sprintf("%s %s %s %s",
		headerMarker.Render(marker),
		bucketDot(r.bucket),
		headerLabel.Render(r.bucket.Label()),
		headerCount.Render(fmt.Sprint(r.count)))
}

// relAge renders a GitKraken-style compact age: 5m, 19h, 6d, 2wk, 4mo.
func relAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 5*7*24*time.Hour:
		return fmt.Sprintf("%dwk", int(d.Hours()/(24*7)))
	default:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	}
}

func (m Model) renderSubtask(s data.Subtask) string {
	line := fmt.Sprintf("    └ %s %s",
		statusBadge(s.StatusCategory, s.Status), hyperlink(s.URL, keyStyle.Render(s.Key)))
	if mark := gitMarker(m.linkedPR(s.Key)); mark != "" {
		line += " " + mark
	} else if mark := m.branchMarker(s.Key); mark != "" {
		line += " " + mark
	}
	return line + " " + s.Summary
}
