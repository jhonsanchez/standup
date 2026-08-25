// Package chat streams conversations with Claude Code running headless in a
// repo directory. Commenting on Jira, editing code, etc. are all done by the
// user's own Claude Code setup (skills, permissions) — standup only relays.
package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Event is one streamed chat event.
type Event struct {
	Delta     string // assistant text (may be a whole block)
	Tool      string // non-empty: tool activity note, e.g. `Bash: curl …`
	SessionID string // set on completion
	Err       error
	Done      bool
}

// Stream is a running claude turn.
type Stream struct {
	Events chan Event
	Cancel context.CancelFunc
}

// Send starts one headless claude turn in dir, resuming sessionID when set.
// systemAppend (first turn) injects ticket context via --append-system-prompt.
// permMode is required headless — the default mode would hang on prompts.
func Send(dir, sessionID, systemAppend, permMode, prompt string, extraEnv, allowedTools []string) (*Stream, error) {
	ctx, cancel := context.WithCancel(context.Background())
	if permMode == "" {
		permMode = "acceptEdits"
	}
	args := []string{"-p", prompt, "--output-format", "stream-json", "--verbose",
		"--permission-mode", permMode}
	if systemAppend != "" {
		args = append(args, "--append-system-prompt", systemAppend)
	}
	// Project-scoped MCP servers (.mcp.json) need interactive approval,
	// which headless runs can't give — pass the file explicitly to load
	// them, and auto-allow each declared server (mcp__<name> covers all of
	// its tools): a repo declaring a server is opting into it.
	if mcp := filepath.Join(dir, ".mcp.json"); fileExists(mcp) {
		args = append(args, "--mcp-config", mcp)
		for _, name := range mcpServerNames(mcp) {
			allowedTools = append(allowedTools, "mcp__"+name)
		}
	}
	seen := map[string]bool{}
	for _, t := range allowedTools {
		if seen[t] {
			continue
		}
		seen[t] = true
		args = append(args, "--allowedTools", t)
	}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	debugLog(fmt.Sprintf("send dir=%s resume=%.8s env=%d args=%q", dir, sessionID, len(extraEnv), args))
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = dir
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}

	events := make(chan Event, 64)
	go func() {
		defer close(events)
		defer cmd.Wait()
		toolNames := map[string]string{}
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
		session := sessionID
		gotResult := false
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || !strings.HasPrefix(line, "{") {
				continue
			}
			var ev streamEvent
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				continue
			}
			if ev.SessionID != "" {
				session = ev.SessionID
			}
			switch ev.Type {
			case "assistant":
				for _, c := range ev.Message.Content {
					switch c.Type {
					case "text":
						if c.Text != "" {
							events <- Event{Delta: c.Text}
						}
					case "tool_use":
						toolNames[c.ID] = c.Name
						events <- Event{Tool: toolNote(c.Name, c.Input)}
					}
				}
			case "user":
				// Tool results ride back as user events; surface errors
				// (permission denials, failures) so blockage is visible.
				for _, c := range ev.Message.Content {
					if c.Type == "tool_result" && c.IsError {
						name := toolNames[c.ToolUseID]
						if name == "" {
							name = "tool"
						}
						events <- Event{Tool: "✗ " + name + ": " + compact(c.Text, 110)}
					}
				}
			case "result":
				gotResult = true
				debugLog(fmt.Sprintf("result session=%.8s is_error=%v denials=%d subtype=%s",
					session, ev.IsError, len(ev.PermissionDenials), ev.Subtype))
				if len(ev.PermissionDenials) > 0 {
					names := map[string]bool{}
					var list []string
					for _, d := range ev.PermissionDenials {
						if d.ToolName != "" && !names[d.ToolName] {
							names[d.ToolName] = true
							list = append(list, d.ToolName)
						}
					}
					if len(list) > 0 {
						events <- Event{Tool: "✗ denied this turn: " + strings.Join(list, ", ") +
							" — add to chat_allowed_tools"}
					}
				}
				var err error
				if ev.IsError {
					err = fmt.Errorf("%s", firstNonEmpty(ev.Result, ev.Subtype, "claude returned an error"))
				}
				events <- Event{Done: true, SessionID: session, Err: err}
			}
		}
		if !gotResult {
			err := ctx.Err() // cancelled, or the process died mid-stream
			if err == nil {
				err = fmt.Errorf("claude exited without a result — is `claude` logged in?")
			} else {
				err = nil // user-cancelled: not an error
			}
			events <- Event{Done: true, SessionID: session, Err: err}
		}
	}()
	return &Stream{Events: events, Cancel: cancel}, nil
}

type streamEvent struct {
	Type              string `json:"type"`
	Subtype           string `json:"subtype"`
	SessionID         string `json:"session_id"`
	Result            string `json:"result"`
	IsError           bool   `json:"is_error"`
	PermissionDenials []struct {
		ToolName string `json:"tool_name"`
	} `json:"permission_denials"`
	Message struct {
		Content []contentBlock `json:"content"`
	} `json:"message"`
}

// toolNote compacts a tool_use into a one-line activity note.
func toolNote(name string, input json.RawMessage) string {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	detail := ""
	for _, k := range []string{"command", "file_path", "path", "pattern", "url", "description", "query"} {
		if v, ok := in[k].(string); ok && v != "" {
			detail = v
			break
		}
	}
	detail = strings.Join(strings.Fields(detail), " ")
	if len(detail) > 70 {
		detail = detail[:70] + "…"
	}
	if detail == "" {
		return name
	}
	return name + ": " + detail
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

type contentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Text      string          `json:"text"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// compact flattens whitespace and caps length for one-line display.
func compact(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// debugLog appends chat diagnostics to ~/.local/state/standup/chat-debug.log
// so "what did the TUI actually execute" is answerable after the fact.
func debugLog(line string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".local", "state", "standup")
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "chat-debug.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("15:04:05"), line)
}

// mcpServerNames lists the server names declared in a .mcp.json file.
func mcpServerNames(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil
	}
	names := make([]string, 0, len(doc.MCPServers))
	for name := range doc.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
