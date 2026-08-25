package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"github.com/jhonsanchez/standup/internal/data"
)

var (
	colPurple = lipgloss.Color("135")
	colDim    = lipgloss.Color("241")
	colFaint  = lipgloss.Color("238")
	colBlue   = lipgloss.Color("75")
	colGreen  = lipgloss.Color("77")
	colOrange = lipgloss.Color("214")
	colRed    = lipgloss.Color("203")
	colWhite  = lipgloss.Color("252")

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colWhite).Padding(0, 1)

	clientTabActive = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("231")).Background(colPurple).Padding(0, 2)
	clientTabInactive = lipgloss.NewStyle().Foreground(colDim).Padding(0, 2)

	viewTabActive = lipgloss.NewStyle().Bold(true).Foreground(colPurple).
			Underline(true).Padding(0, 1)
	viewTabInactive = lipgloss.NewStyle().Foreground(colDim).Padding(0, 1)

	countStyle = lipgloss.NewStyle().Foreground(colDim)

	cursorLineStyle = lipgloss.NewStyle().Background(lipgloss.Color("236"))

	keyStyle     = lipgloss.NewStyle().Foreground(colBlue)
	subtaskStyle = lipgloss.NewStyle().Foreground(colDim)
	repoStyle    = lipgloss.NewStyle().Foreground(colDim)
	helpStyle    = lipgloss.NewStyle().Foreground(colFaint).Padding(0, 1)
	errStyle     = lipgloss.NewStyle().Foreground(colRed).Padding(0, 1)
	emptyStyle   = lipgloss.NewStyle().Foreground(colDim).Padding(1, 2)

	badgeTodo   = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("240")).Padding(0, 1)
	badgeProg   = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("26")).Padding(0, 1)
	badgeDone   = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("28")).Padding(0, 1)
	badgeReview = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(colOrange).Padding(0, 1)
	badgeDraft  = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("240")).Padding(0, 1)
	badgeOpen   = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("28")).Padding(0, 1)
)

var (
	ageStyle     = lipgloss.NewStyle().Foreground(colDim)
	addStyle     = lipgloss.NewStyle().Foreground(colGreen)
	delStyle     = lipgloss.NewStyle().Foreground(colRed)
	headerMarker = lipgloss.NewStyle().Foreground(colDim)
	headerLabel  = lipgloss.NewStyle().Bold(true).Foreground(colWhite)
	headerCount  = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).
			Background(lipgloss.Color("238")).Padding(0, 1)

	iconPass    = lipgloss.NewStyle().Foreground(colGreen).Render("✓")
	iconFail    = lipgloss.NewStyle().Foreground(colRed).Render("✗")
	iconPending = lipgloss.NewStyle().Foreground(colOrange).Render("●")
	iconNone    = lipgloss.NewStyle().Foreground(colFaint).Render("·")
	iconClash   = lipgloss.NewStyle().Foreground(colOrange).Render("⚠")

	linkStyle = lipgloss.NewStyle().Foreground(colPurple)
)

// Marker glyphs: Nerd Font by default (branch U+E0A0, octocat U+F408),
// plain-unicode fallbacks with `icons: ascii`.
var (
	branchGlyph = ""
	githubGlyph = ""
	mergedGlyph = "\ue727" // nerd-font git-merge
)

// mergedStyle is GitHub's "merged" purple.
var mergedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("170"))

func setIcons(mode string) {
	if mode == "ascii" {
		branchGlyph = "⎇"
		githubGlyph = "⇄"
		mergedGlyph = "⇌"
	} else {
		branchGlyph = "\ue0a0"
		githubGlyph = "\uf408"
		mergedGlyph = "\ue727"
	}
}

func bucketDot(b data.Bucket) string {
	var c lipgloss.Color
	switch b {
	case data.BucketNeedsMyReview:
		c = colOrange
	case data.BucketWaitingForReview:
		c = colDim
	case data.BucketReadyToMerge:
		c = colGreen
	case data.BucketResolveConflicts:
		c = colOrange
	case data.BucketFailingCI:
		c = colRed
	case data.BucketReviewerCommented:
		c = colBlue
	default:
		c = colFaint
	}
	return lipgloss.NewStyle().Foreground(c).Render("●")
}

func ciIcon(state string) string {
	switch state {
	case "SUCCESS":
		return iconPass
	case "FAILURE", "ERROR":
		return iconFail
	case "PENDING", "EXPECTED":
		return iconPending
	default:
		return iconNone
	}
}

// reviewLabel renders a PR's review decision as a compact colored label.
func reviewLabel(decision string) string {
	switch decision {
	case "APPROVED":
		return lipgloss.NewStyle().Foreground(colGreen).Render("approved")
	case "CHANGES_REQUESTED":
		return lipgloss.NewStyle().Foreground(colOrange).Render("changes requested")
	case "REVIEW_REQUIRED":
		return lipgloss.NewStyle().Foreground(colBlue).Render("in review")
	}
	return ""
}

func conflictIcon(mergeable string) string {
	if mergeable == "CONFLICTING" {
		return iconClash
	}
	return " "
}

func diffStat(add, del int) string {
	return addStyle.Render(fmt.Sprintf("+%d", add)) + " " + delStyle.Render(fmt.Sprintf("−%d", del))
}

func statusBadge(category, status string) string {
	switch category {
	case "indeterminate":
		return badgeProg.Render(status)
	case "done":
		return badgeDone.Render(status)
	default:
		return badgeTodo.Render(status)
	}
}

// hyperlink wraps already-styled text in an OSC 8 terminal hyperlink, so
// supporting terminals (iTerm2, WezTerm, kitty) make it clickable.
func hyperlink(url, text string) string {
	if url == "" {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}
