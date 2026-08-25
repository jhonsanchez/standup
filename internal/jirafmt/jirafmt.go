// Package jirafmt renders Jira rich text — wiki markup (Data Center v2) and
// Atlassian Document Format (Cloud v3) — as styled terminal text.
package jirafmt

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	codeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("179")).Background(lipgloss.Color("236"))
	boldStyle    = lipgloss.NewStyle().Bold(true)
	italicStyle  = lipgloss.NewStyle().Italic(true)
	headingStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	quotePrefix = dimStyle.Render("▏ ")
	hrLine      = dimStyle.Render("────────")
)

// ---- Wiki markup (Jira Data Center / Server) ----

var (
	reCodeOpen = regexp.MustCompile(`^\{(code|noformat)(?::[^}]*)?\}`)
	reHeading  = regexp.MustCompile(`^h([1-6])\.\s*`)
	reListItem = regexp.MustCompile(`^([*#-]+)\s+`)
	reInline   = regexp.MustCompile(`\{\{(.+?)\}\}`)
	reImage    = regexp.MustCompile(`!([^!\s][^!]*)!`)
	reUserRef  = regexp.MustCompile(`\[~([^\]]+)\]`)
	reLink     = regexp.MustCompile(`\[([^|\]]+)\|([^\]]+)\]`)
	reBareLink = regexp.MustCompile(`\[(https?://[^\]]+)\]`)
	reColor    = regexp.MustCompile(`\{color(?::[^}]*)?\}`)
	rePanel    = regexp.MustCompile(`\{(panel|quote|anchor)(?::[^}]*)?\}`)
	reBold     = regexp.MustCompile(`\*([^*\n]+)\*`)
	reItalic   = regexp.MustCompile(`\b_([^_\n]+)_\b`)
)

// Wiki renders Jira wiki markup ({{code}}, {code} blocks, h1., *bold*, …).
func Wiki(src string) string {
	var out []string
	inCode := false
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)

		if reCodeOpen.MatchString(trimmed) {
			rest := reCodeOpen.ReplaceAllString(trimmed, "")
			if inCode {
				inCode = false // closing tag line
			} else {
				// Opens a block, unless the closer is on the same line
				// ({code}x{code}).
				inCode = !strings.Contains(rest, "{code}") && !strings.Contains(rest, "{noformat}")
			}
			if content := strings.TrimSpace(stripClose(rest)); content != "" {
				out = append(out, "  "+codeStyle.Render(content))
			}
			continue
		}
		if inCode {
			out = append(out, "  "+codeStyle.Render(line))
			continue
		}

		switch {
		case trimmed == "----":
			out = append(out, hrLine)
			continue
		case strings.HasPrefix(trimmed, "bq. "):
			out = append(out, quotePrefix+renderInline(strings.TrimPrefix(trimmed, "bq. ")))
			continue
		}
		if h := reHeading.FindStringSubmatch(trimmed); h != nil {
			out = append(out, headingStyle.Render(reHeading.ReplaceAllString(trimmed, "")))
			continue
		}
		if l := reListItem.FindStringSubmatch(trimmed); l != nil {
			depth := len(l[1]) - 1
			out = append(out, strings.Repeat("  ", depth)+"• "+renderInline(trimmed[len(l[0]):]))
			continue
		}
		out = append(out, renderInline(line))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func stripClose(s string) string {
	s = strings.ReplaceAll(s, "{code}", "")
	return strings.ReplaceAll(s, "{noformat}", "")
}

func renderInline(s string) string {
	s = reColor.ReplaceAllString(s, "")
	s = rePanel.ReplaceAllString(s, "")
	s = reImage.ReplaceAllString(s, dimStyle.Render("[img: $1]"))
	s = reUserRef.ReplaceAllString(s, "@$1")
	s = reLink.ReplaceAllString(s, "$1 "+dimStyle.Render("($2)"))
	s = reBareLink.ReplaceAllString(s, dimStyle.Render("$1"))
	s = reInline.ReplaceAllStringFunc(s, func(m string) string {
		return codeStyle.Render(strings.TrimSuffix(strings.TrimPrefix(m, "{{"), "}}"))
	})
	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		return boldStyle.Render(strings.Trim(m, "*"))
	})
	s = reItalic.ReplaceAllStringFunc(s, func(m string) string {
		return italicStyle.Render(strings.Trim(m, "_"))
	})
	return s
}

// ---- GitHub Markdown (PR bodies and comments) ----

var (
	reMdHeading  = regexp.MustCompile(`^#{1,6}\s+`)
	reMdBullet   = regexp.MustCompile(`^(\s*)[-*+]\s+`)
	reMdOrdered  = regexp.MustCompile(`^(\s*)(\d+)\.\s+`)
	reMdTask     = regexp.MustCompile(`^(\s*)[-*+]\s+\[([ xX])\]\s+`)
	reMdCode     = regexp.MustCompile("`([^`\n]+)`")
	reMdBold     = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	reMdItalic   = regexp.MustCompile(`(^|[^*])\*([^*\n]+)\*`)
	reMdImage    = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	reMdLink     = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reMdHTMLNote = regexp.MustCompile(`<!--.*?-->`)
)

// Markdown renders GitHub-flavored Markdown as styled terminal text.
func Markdown(src string) string {
	var out []string
	inFence := false
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			out = append(out, "  "+codeStyle.Render(line))
			continue
		}

		switch {
		case trimmed == "---" || trimmed == "***" || trimmed == "___":
			out = append(out, hrLine)
			continue
		case strings.HasPrefix(trimmed, ">"):
			out = append(out, quotePrefix+mdInline(strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))))
			continue
		}
		if reMdHeading.MatchString(trimmed) {
			out = append(out, headingStyle.Render(reMdHeading.ReplaceAllString(trimmed, "")))
			continue
		}
		if t := reMdTask.FindStringSubmatch(line); t != nil {
			box := "☐"
			if t[2] != " " {
				box = "☑"
			}
			out = append(out, t[1]+box+" "+mdInline(line[len(t[0]):]))
			continue
		}
		if b := reMdBullet.FindStringSubmatch(line); b != nil {
			out = append(out, b[1]+"• "+mdInline(line[len(b[0]):]))
			continue
		}
		if o := reMdOrdered.FindStringSubmatch(line); o != nil {
			out = append(out, o[1]+o[2]+". "+mdInline(line[len(o[0]):]))
			continue
		}
		out = append(out, mdInline(line))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func mdInline(s string) string {
	s = reMdHTMLNote.ReplaceAllString(s, "")
	s = reMdImage.ReplaceAllString(s, dimStyle.Render("[img: $1]"))
	s = reMdLink.ReplaceAllString(s, "$1 "+dimStyle.Render("($2)"))
	s = reMdCode.ReplaceAllStringFunc(s, func(m string) string {
		return codeStyle.Render(strings.Trim(m, "`"))
	})
	s = reMdBold.ReplaceAllStringFunc(s, func(m string) string {
		return boldStyle.Render(strings.Trim(m, "*"))
	})
	s = reMdItalic.ReplaceAllString(s, "$1"+italicStyle.Render("$2"))
	return s
}

// ---- ADF (Jira Cloud) ----

type adfNode struct {
	Type    string            `json:"type"`
	Text    string            `json:"text"`
	Content []json.RawMessage `json:"content"`
	Marks   []struct {
		Type  string `json:"type"`
		Attrs struct {
			Href string `json:"href"`
		} `json:"attrs"`
	} `json:"marks"`
	Attrs struct {
		Text      string `json:"text"`
		ShortName string `json:"shortName"`
		URL       string `json:"url"`
	} `json:"attrs"`
}

// ADF renders an Atlassian Document Format tree as styled terminal text.
func ADF(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return strings.TrimSpace(adf(raw))
}

func adf(raw json.RawMessage) string {
	var n adfNode
	if err := json.Unmarshal(raw, &n); err != nil {
		return ""
	}
	children := func() string {
		var b strings.Builder
		for _, c := range n.Content {
			b.WriteString(adf(c))
		}
		return b.String()
	}

	switch n.Type {
	case "text":
		s := n.Text
		for _, mk := range n.Marks {
			switch mk.Type {
			case "code":
				s = codeStyle.Render(n.Text)
			case "strong":
				s = boldStyle.Render(s)
			case "em":
				s = italicStyle.Render(s)
			case "link":
				s = s + " " + dimStyle.Render("("+mk.Attrs.Href+")")
			}
		}
		return s
	case "paragraph":
		return children() + "\n"
	case "heading":
		return headingStyle.Render(strings.TrimSpace(children())) + "\n"
	case "bulletList", "orderedList":
		var b strings.Builder
		num := 0
		for _, c := range n.Content {
			num++
			item := strings.TrimRight(adf(c), "\n")
			prefix := "• "
			if n.Type == "orderedList" {
				prefix = fmt.Sprintf("%d. ", num)
			}
			for i, line := range strings.Split(item, "\n") {
				if i == 0 {
					b.WriteString(prefix + line + "\n")
				} else {
					b.WriteString("  " + line + "\n")
				}
			}
		}
		return b.String()
	case "codeBlock":
		var b strings.Builder
		for _, line := range strings.Split(strings.TrimRight(children(), "\n"), "\n") {
			b.WriteString("  " + codeStyle.Render(line) + "\n")
		}
		return b.String()
	case "blockquote":
		var b strings.Builder
		for _, line := range strings.Split(strings.TrimRight(children(), "\n"), "\n") {
			b.WriteString(quotePrefix + line + "\n")
		}
		return b.String()
	case "hardBreak":
		return "\n"
	case "rule":
		return hrLine + "\n"
	case "mention":
		return n.Attrs.Text
	case "emoji":
		if n.Attrs.Text != "" {
			return n.Attrs.Text
		}
		return n.Attrs.ShortName
	case "inlineCard":
		return dimStyle.Render(n.Attrs.URL)
	case "mediaSingle", "mediaGroup", "media":
		return dimStyle.Render("[attachment]") + "\n"
	default:
		return children()
	}
}
