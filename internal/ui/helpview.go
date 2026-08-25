package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	helpKeyStyle   = lipgloss.NewStyle().Foreground(colPurple)
	helpArrowStyle = lipgloss.NewStyle().Foreground(colFaint)
	helpGroupStyle = lipgloss.NewStyle().Bold(true).Foreground(colWhite)
)

// viewHelp renders the which-key style shortcuts menu: context-aware, full
// descriptions, groups flowed into columns.
func (m Model) viewHelp() string {
	ctx := ctxList
	ctxName := "list"
	if len(m.detailStack) > 0 {
		ctx = ctxDetail
		ctxName = "detail"
	}

	// Collect visible bindings per group, preserving group order.
	type entry struct{ label, desc string }
	groups := map[string][]entry{}
	labelW := 0
	mp := &m
	for _, b := range m.km.order {
		if b.ctx&ctx == 0 {
			continue
		}
		if b.when != nil && !b.when(mp) {
			continue
		}
		l := b.keyLabel()
		if w := lipgloss.Width(l); w > labelW {
			labelW = w
		}
		groups[b.group] = append(groups[b.group], entry{l, b.desc})
	}

	// Render each group as a block of lines.
	var blocks [][]string
	for _, g := range groupOrder {
		es := groups[g]
		if len(es) == 0 {
			continue
		}
		lines := []string{helpGroupStyle.Render(g)}
		for _, e := range es {
			pad := strings.Repeat(" ", labelW-lipgloss.Width(e.label))
			lines = append(lines, "  "+pad+helpKeyStyle.Render(e.label)+" "+
				helpArrowStyle.Render("→")+" "+e.desc)
		}
		lines = append(lines, "")
		blocks = append(blocks, lines)
	}

	// Legend: the row markers and icons, shown with their real styling.
	legend := []string{
		helpGroupStyle.Render("Legend"),
		"  " + linkStyle.Render(githubGlyph+" repo#12") + " " + helpArrowStyle.Render("→") + " linked PR (purple: in review)",
		"  " + lipgloss.NewStyle().Foreground(colGreen).Render(githubGlyph+" repo#12") + " " + helpArrowStyle.Render("→") + " linked PR approved",
		"  " + lipgloss.NewStyle().Foreground(colOrange).Render(githubGlyph+" repo#12") + " " + helpArrowStyle.Render("→") + " linked PR: changes requested",
		"  " + mergedStyle.Render(mergedGlyph+" repo#12") + " " + helpArrowStyle.Render("→") + " PR merged (icon = post-merge pipeline)",
		"  " + subtaskStyle.Render(branchGlyph+" repo:branch") + " " + helpArrowStyle.Render("→") + " branch exists, no PR yet",
		"  " + iconPass + " " + iconFail + " " + iconPending + " " + iconNone + " " + helpArrowStyle.Render("→") + " CI passing / failing / running / none",
		"  " + iconClash + " " + helpArrowStyle.Render("→") + " merge conflicts",
		"  [1/3] " + helpArrowStyle.Render("→") + " subtasks done/total (→ expands; also lists multiple linked PRs)",
		"  " + alertCommentIcon() + " " + helpArrowStyle.Render("→") + " all PRs merged but no Jira comment",
		"  " + alertStatusIcon() + " " + helpArrowStyle.Render("→") + " all PRs merged but issue status not updated",
		"  " + alertDoneOpenIcon() + " " + helpArrowStyle.Render("→") + " issue Done but PRs still open",
		"  " + alertApprovedIcon() + " " + helpArrowStyle.Render("→") + " PR approved but unmerged too long",
		"  " + alertStaleIcon() + " " + helpArrowStyle.Render("→") + " PR without activity too long",
		"  " + alertBehindIcon() + " " + helpArrowStyle.Render("→") + " branch behind base — update before merge",
		"  " + alertBlockedIcon() + " " + helpArrowStyle.Render("→") + " merge blocked by branch protection",
		"",
	}
	blocks = append(blocks, legend)

	// Flow blocks into columns (greedy: shortest column first).
	width := m.width
	if width <= 0 {
		width = 100
	}
	ncols := width / 62
	if ncols < 1 {
		ncols = 1
	}
	if ncols > 3 {
		ncols = 3
	}
	cols := make([][]string, ncols)
	for _, b := range blocks {
		shortest := 0
		for i := 1; i < ncols; i++ {
			if len(cols[i]) < len(cols[shortest]) {
				shortest = i
			}
		}
		cols[shortest] = append(cols[shortest], b...)
	}
	colW := width/ncols - 2
	var rendered []string
	for _, c := range cols {
		if len(c) == 0 {
			continue
		}
		col := lipgloss.NewStyle().Width(colW).Render(strings.Join(c, "\n"))
		rendered = append(rendered, col)
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, rendered...)

	title := headerLabel.Render("Keyboard shortcuts") + subtaskStyle.Render("  ("+ctxName+" view — only currently available keys are shown)")
	footer := helpKeyStyle.Render("<esc>") + helpArrowStyle.Render(" close   ") +
		helpKeyStyle.Render("<any key>") + helpArrowStyle.Render(" close and run it")

	content := title + "\n\n" + body
	avail := m.termHeight() - 2
	content = padToHeight(content, avail)
	return lipgloss.NewStyle().Padding(0, 1).Render(content + "\n" + footer)
}
