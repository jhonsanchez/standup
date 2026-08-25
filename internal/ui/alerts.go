package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jhonsanchez/standup/internal/data"
)

// Policy alerts: cheap, data-local nudges about work in an inconsistent
// state. Each has a compact single-width glyph for list rows; the detail
// view and ? legend spell out the reason.

type alert struct {
	icon   string
	reason string
}

var (
	alertOrange = lipgloss.NewStyle().Foreground(colOrange)
	alertRed    = lipgloss.NewStyle().Foreground(colRed)
)

func alertCommentIcon() string { return alertOrange.Render(commentGlyph + "!") }
func alertStatusIcon() string  { return alertOrange.Render("⧗") }
func alertDoneOpenIcon() string {
	return alertRed.Render("⚑")
}
func alertApprovedIcon() string { return alertOrange.Render("⇡") }
func alertBehindIcon() string   { return alertOrange.Render("⇣") }
func alertBlockedIcon() string  { return alertRed.Render("⊘") }
func alertStaleIcon() string    { return alertOrange.Render("◷") }

// issueAlerts inspects a Jira issue against its linked PRs.
func (m Model) issueAlerts(it data.Item) []alert {
	if it.Kind != data.KindJiraIssue {
		return nil
	}
	prs := m.linkedPRs(it.Key)
	if len(prs) == 0 {
		return nil
	}
	allMerged, anyOpen := true, false
	for _, p := range prs {
		if !p.Merged {
			allMerged = false
			anyOpen = true
		}
	}
	var out []alert
	if allMerged && it.CommentCount == 0 {
		out = append(out, alert{alertCommentIcon(), "all PRs merged but no Jira comment — add a wrap-up comment"})
	}
	if allMerged && it.StatusCategory != "done" {
		out = append(out, alert{alertStatusIcon(), "all PRs merged but the issue is still " + it.Status + " — update its status"})
	}
	if it.StatusCategory == "done" && anyOpen {
		out = append(out, alert{alertDoneOpenIcon(), "issue is Done but has open PRs — merge or close them"})
	}
	return out
}

// prAlerts inspects one open PR's staleness.
func (m Model) prAlerts(p data.Item) []alert {
	if p.Kind != data.KindPullRequest || p.Merged || p.Updated.IsZero() {
		return nil
	}
	approvedDays, staleDays := m.cfg.AlertDays()
	age := time.Since(p.Updated)
	var out []alert
	switch p.MergeState {
	case "BEHIND":
		out = append(out, alert{alertBehindIcon(), "branch is behind the base — update/rebase before merging"})
	case "BLOCKED":
		out = append(out, alert{alertBlockedIcon(), "merge blocked by branch protection (reviews/checks/rules)"})
	}
	if approvedDays > 0 && p.ReviewDecision == "APPROVED" && age > time.Duration(approvedDays)*24*time.Hour {
		out = append(out, alert{alertApprovedIcon(),
			fmt.Sprintf("approved but unmerged for %s — merge it", relAge(p.Updated))})
	} else if staleDays > 0 && !p.Draft && age > time.Duration(staleDays)*24*time.Hour {
		out = append(out, alert{alertStaleIcon(),
			fmt.Sprintf("no activity for %s — nudge reviewers?", relAge(p.Updated))})
	}
	return out
}

func alertIcons(alerts []alert) []string {
	var out []string
	for _, a := range alerts {
		out = append(out, a.icon)
	}
	return out
}
