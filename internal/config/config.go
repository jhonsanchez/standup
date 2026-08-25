package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Clients []Client `yaml:"clients"`
	// Refresh is the background auto-refresh interval for the active client
	// as a Go duration ("30s", "2m"). Default 30s; "off" disables; values
	// under 10s are clamped to 10s (GitHub search throttles rapid repeats).
	Refresh string `yaml:"refresh,omitempty"`
	// RefreshMinutes is the older integer form; negative disables.
	RefreshMinutes int `yaml:"refresh_minutes,omitempty"`
	// Icons selects the marker glyph set: "nerd" (default, needs a Nerd Font
	// such as JetBrainsMono Nerd Font) or "ascii".
	Icons string `yaml:"icons,omitempty"`
	// LinkPattern is the regex that extracts issue keys from PR titles and
	// branch names for issue⇄PR linking. Default: Jira-style [A-Z][A-Z0-9]+-\d+.
	LinkPattern string   `yaml:"link_pattern,omitempty"`
	Commands    Commands `yaml:"commands,omitempty"`
	// HideClients shows only the active client in the header — other client
	// names never render on screen (switch with number keys as usual).
	// Useful when screen-sharing with one client. For full isolation run
	// `standup <client>`, which loads only that client.
	HideClients bool `yaml:"hide_clients,omitempty"`
	// AutoUpdate silently updates direct-binary installs in the background
	// when a new release is available (applies on next launch). Brew and
	// go-install builds are never touched. Default true; set false to only
	// show the update notice.
	AutoUpdate *bool `yaml:"auto_update,omitempty"`
	// BranchTemplate names branches created by the b start-work flow;
	// {key} is the issue key. Default "{key}" (e.g. FALCON-3149).
	BranchTemplate string `yaml:"branch_template,omitempty"`
	// Alerts tunes the policy-alert thresholds (days). 0 → defaults
	// (approved_days: 3, stale_review_days: 5); negative disables one.
	Alerts Alerts `yaml:"alerts,omitempty"`
	// Keys remaps shortcuts by action id, e.g. {git-view: "G", filter: "s"}.
	// Press ? in the app to see actions; ids are documented in the README.
	Keys map[string]string `yaml:"keys,omitempty"`
}

// Alerts configures policy-alert thresholds.
type Alerts struct {
	ApprovedDays    int `yaml:"approved_days,omitempty"`
	StaleReviewDays int `yaml:"stale_review_days,omitempty"`
}

// AlertDays returns (approved-but-unmerged, stale-review) thresholds in
// days; negative config values disable an alert (returned as 0).
func (c *Config) AlertDays() (approved, stale int) {
	approved, stale = c.Alerts.ApprovedDays, c.Alerts.StaleReviewDays
	if approved == 0 {
		approved = 3
	}
	if stale == 0 {
		stale = 5
	}
	if approved < 0 {
		approved = 0
	}
	if stale < 0 {
		stale = 0
	}
	return approved, stale
}

// Commands configures the external tools launched from the detail view.
type Commands struct {
	// Agent is the AI coding agent for the `a` key: "claude" (default),
	// "copilot", or any command line; "{prompt}" is replaced with the issue
	// context (appended as the last argument if absent).
	Agent string `yaml:"agent,omitempty"`
	// GitUI is the git TUI for the `L` key (default "lazygit").
	GitUI string `yaml:"git_ui,omitempty"`
	// ChatPermissionMode is claude's --permission-mode for the in-app chat
	// (default "acceptEdits"; use "plan" for read-only chat).
	ChatPermissionMode string `yaml:"chat_permission_mode,omitempty"`
}

// ChatPermissionMode is the --permission-mode for the in-app chat's headless
// claude runs. Default acceptEdits (edits auto-approved; other actions follow
// your Claude settings allowlists).
func (c *Config) ChatPermissionMode() string {
	if c.Commands.ChatPermissionMode != "" {
		return c.Commands.ChatPermissionMode
	}
	return "acceptEdits"
}

// AutoUpdateEnabled reports whether background self-update is on (default).
func (c *Config) AutoUpdateEnabled() bool {
	return c.AutoUpdate == nil || *c.AutoUpdate
}

// AgentCommand returns the argv for the AI agent with the prompt applied.
func (c *Config) AgentCommand(prompt string) []string {
	agent := c.Commands.Agent
	switch agent {
	case "", "claude":
		return []string{"claude", prompt}
	case "copilot":
		return []string{"copilot", "-p", prompt}
	}
	parts := strings.Fields(agent)
	replaced := false
	for i, p := range parts {
		if strings.Contains(p, "{prompt}") {
			parts[i] = strings.ReplaceAll(p, "{prompt}", prompt)
			replaced = true
		}
	}
	if !replaced {
		parts = append(parts, prompt)
	}
	return parts
}

// GitUICommand returns the git TUI binary for the `L` key.
func (c *Config) GitUICommand() string {
	if c.Commands.GitUI != "" {
		return c.Commands.GitUI
	}
	return "lazygit"
}

// RefreshInterval returns the auto-refresh cadence (0 = disabled).
func (c *Config) RefreshInterval() time.Duration {
	if c.Refresh == "off" || c.RefreshMinutes < 0 {
		return 0
	}
	d := 30 * time.Second
	if c.Refresh != "" {
		if parsed, err := time.ParseDuration(c.Refresh); err == nil {
			d = parsed
		}
	} else if c.RefreshMinutes > 0 {
		d = time.Duration(c.RefreshMinutes) * time.Minute
	}
	if d < 10*time.Second {
		d = 10 * time.Second
	}
	return d
}

type Client struct {
	Name   string  `yaml:"name"`
	Jira   *Jira   `yaml:"jira,omitempty"`
	GitHub *GitHub `yaml:"github,omitempty"`
	// ProjectsDir is where this client's repos are cloned locally.
	// Defaults to ~/projects/<name>. Used by the checkout action.
	ProjectsDir string `yaml:"projects_dir,omitempty"`
	// RepoMap maps a Jira project key to a default repo folder for items
	// with no linked PR or branch yet (e.g. FALCON: zpc-system-test).
	RepoMap map[string]string `yaml:"repo_map,omitempty"`
	// Env is extra environment for every subprocess standup launches for
	// this client (chat, agent, terminal, git UI) — e.g. a per-client
	// CLAUDE_CONFIG_DIR so claude uses a client-specific profile.
	Env map[string]string `yaml:"env,omitempty"`
	// ChatAllowedTools pre-approves tools for the headless chat
	// (--allowedTools). Headless runs cannot show permission prompts, so
	// MCP tools like mcp__jira__add_comment must be listed here (or in the
	// profile's settings.json) to work from the chat.
	ChatAllowedTools []string `yaml:"chat_allowed_tools,omitempty"`
}

// EnvList renders Env as KEY=VALUE pairs, expanding a leading ~/ in values.
func (c *Client) EnvList() []string {
	var out []string
	for k, v := range c.Env {
		if strings.HasPrefix(v, "~/") {
			home, _ := os.UserHomeDir()
			v = filepath.Join(home, v[2:])
		}
		out = append(out, k+"="+v)
	}
	return out
}

// ProjectsBase returns the directory holding this client's local clones.
func (c *Client) ProjectsBase() string {
	base := c.ProjectsDir
	if base == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "projects", c.Name)
	}
	if strings.HasPrefix(base, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, base[2:])
	}
	return base
}

// RepoDir returns the local clone of repo ("owner/name") if it exists.
func (c *Client) RepoDir(repo string) (string, bool) {
	base := c.ProjectsBase()
	short := repo
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		short = repo[i+1:]
	}
	dir := filepath.Join(base, short)
	if st, err := os.Stat(filepath.Join(dir, ".git")); err == nil && st.IsDir() {
		return dir, true
	}
	return dir, false
}

type Jira struct {
	BaseURL string `yaml:"base_url"`
	// Flavor is "cloud" (Atlassian Cloud: email + API token, REST v3) or
	// "datacenter" (self-hosted: PAT bearer auth, REST v2). Defaults to
	// cloud when email is set, datacenter otherwise.
	Flavor   string `yaml:"flavor,omitempty"`
	Email    string `yaml:"email,omitempty"`
	Token    string `yaml:"token,omitempty"`
	TokenEnv string `yaml:"token_env,omitempty"`
	// Projects limits the query to these project keys (comma-separated).
	Projects string `yaml:"projects,omitempty"`
	// JQL overrides the default "my issues in open sprints" query.
	JQL string `yaml:"jql,omitempty"`
}

func (j *Jira) IsDataCenter() bool {
	if j.Flavor != "" {
		return j.Flavor == "datacenter" || j.Flavor == "server"
	}
	return j.Email == ""
}

type GitHub struct {
	Host     string   `yaml:"host,omitempty"` // default github.com
	Token    string   `yaml:"token,omitempty"`
	TokenEnv string   `yaml:"token_env,omitempty"`
	Orgs     []string `yaml:"orgs,omitempty"`
	Repos    []string `yaml:"repos,omitempty"` // owner/repo entries
}

func (j *Jira) ResolveToken() (string, error) {
	if j.Token != "" {
		return j.Token, nil
	}
	if j.TokenEnv != "" {
		if v := os.Getenv(j.TokenEnv); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("env var %s is empty", j.TokenEnv)
	}
	return "", fmt.Errorf("jira: no token or token_env configured")
}

// ResolveToken returns a GitHub token from config, env, or `gh auth token`.
func (g *GitHub) ResolveToken() (string, error) {
	if g.Token != "" {
		return g.Token, nil
	}
	if g.TokenEnv != "" {
		if v := os.Getenv(g.TokenEnv); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("env var %s is empty", g.TokenEnv)
	}
	host := g.Host
	if host == "" {
		host = "github.com"
	}
	out, err := exec.Command("gh", "auth", "token", "--hostname", host).Output()
	if err != nil {
		return "", fmt.Errorf("github: no token configured and `gh auth token` failed")
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *GitHub) APIBase() string {
	if g.Host == "" || g.Host == "github.com" {
		return "https://api.github.com"
	}
	return "https://" + g.Host + "/api/v3"
}

func Path() string {
	if p := os.Getenv("STANDUP_CONFIG"); p != "" {
		return p
	}
	if p := os.Getenv("LAUNCHPAD_CONFIG"); p != "" { // pre-rename compatibility
		return p
	}
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".config", "standup", "config.yaml")
	if _, err := os.Stat(path); err != nil {
		// Fall back to the pre-rename location if it exists.
		old := filepath.Join(home, ".config", "launchpad", "config.yaml")
		if _, err := os.Stat(old); err == nil {
			return old
		}
	}
	return path
}

// ErrCreated signals that a starter config was written and needs editing.
type ErrCreated struct{ Path string }

func (e ErrCreated) Error() string {
	return "created starter config at " + e.Path + " — edit it, then run standup again"
}

func Load() (*Config, error) {
	path := Path()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(starterConfig), 0o600); err != nil {
			return nil, err
		}
		return nil, ErrCreated{Path: path}
	}
	if err != nil {
		return nil, err
	}
	b = expandEnv(b)
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(cfg.Clients) == 0 {
		return nil, fmt.Errorf("%s has no clients configured", path)
	}
	return &cfg, nil
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv replaces ${VAR} references in the config with environment values.
// Unset variables expand to empty so a missing token fails loudly downstream.
func expandEnv(b []byte) []byte {
	return envRef.ReplaceAllFunc(b, func(m []byte) []byte {
		name := envRef.FindSubmatch(m)[1]
		return []byte(os.Getenv(string(name)))
	})
}

const starterConfig = `# yaml-language-server: $schema=https://raw.githubusercontent.com/jhonsanchez/standup/main/schema/config.schema.json
# standup config
# Each client gets its own tab. jira and github sections are both optional.
# ${VAR} references anywhere in this file are expanded from the environment.
#
# Icons use Nerd Font glyphs — install "JetBrainsMono Nerd Font" (or any Nerd
# Font) for best results, or set: icons: ascii
#
# Jira Cloud auth: email + API token
#   (https://id.atlassian.com/manage-profile/security/api-tokens)
# Jira Data Center auth: flavor: datacenter + a Personal Access Token.
# GitHub auth: leave token/token_env out to use your logged-in gh CLI
#   (gh auth token), or set token_env to an env var holding a PAT.

# refresh: 30s          # auto-refresh of the active tab; "off" to disable
# icons: nerd           # or: ascii
# commands:
#   agent: claude       # AI agent for the 'a' key: claude | copilot | custom
#   git_ui: lazygit     # git TUI for the 'L' key
# keys:                 # remap shortcuts by action id (press ? in the app)
#   git-view: G
#   filter: s

clients:
  - name: work
    jira:
      base_url: https://YOURSITE.atlassian.net
      email: you@example.com          # omit + set flavor: datacenter for self-hosted
      token_env: WORK_JIRA_TOKEN
      projects: PROJ                  # optional: limit to project keys
      # jql: ...                      # optional: replace the sprint query (e.g. Kanban)
    github:
      orgs: [your-org]                # and/or repos: [owner/repo]
    # projects_dir: ~/projects/work   # local clones (default ~/projects/<name>)

  - name: personal
    github: {}   # no org filter: everything assigned to / authored by you
`
