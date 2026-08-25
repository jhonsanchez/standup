# standup

A terminal dashboard for your work across companies/clients. Each client gets
a tab that merges **Jira sprint issues** (with subtasks) and **GitHub pull
requests / issues** into two views — `ISSUES` and `PULL REQUESTS` — with
issue⇄PR⇄branch cross-linking and one-key jumps into your local repos.

## Install

### Linux

- **Homebrew** (Linuxbrew): `brew install jhonsanchez/tap/standup`
- **install script** — detects arch, always fetches the latest release:
  ```sh
  curl -fsSL https://raw.githubusercontent.com/jhonsanchez/standup/main/install.sh | sh
  ```
- **Go**: `go install github.com/jhonsanchez/standup@latest` (lands in `~/go/bin`)

### macOS

- **Homebrew**: `brew install jhonsanchez/tap/standup`
- **install script** (detects Apple Silicon vs Intel):
  ```sh
  curl -fsSL https://raw.githubusercontent.com/jhonsanchez/standup/main/install.sh | sh
  ```
- **Go**: `go install github.com/jhonsanchez/standup@latest`

### Windows

- **Manual**: download `standup_<version>_windows_amd64.zip` (or `arm64`) from
  [Releases](https://github.com/jhonsanchez/standup/releases), unzip, and put
  `standup.exe` on your `PATH`. Use Windows Terminal for correct rendering.
- **Go**: `go install github.com/jhonsanchez/standup@latest`

### Updating

**Auto-update**: release builds installed as a direct binary (the install
script's default) keep themselves current — when a new release exists,
standup downloads it in the background (sha256-verified against the release
checksums), swaps the binary, and shows `✓ auto-updated — restart to apply`
in the footer. Brew/go installs just get an update notice. Opt out with
`auto_update: false` in the config.

**Manually**: `standup upgrade` detects the install method and updates in place
(`brew upgrade standup` for brew installs, `go install @latest` for Go
installs, direct binary swap otherwise; on Windows it points you to the
releases page).

### Dependencies

The binary is self-contained — everything below is optional and only lights
up a specific feature. `standup doctor` reports what's missing.

| Dependency | Used for | Install |
| --- | --- | --- |
| **Nerd Font** (JetBrainsMono recommended) | marker glyphs `` `` | `brew install --cask font-jetbrains-mono-nerd-font` · Ubuntu: unzip from [nerdfonts.com](https://www.nerdfonts.com) into `~/.local/share/fonts` + `fc-cache -f` · or set `icons: ascii` |
| `git` | checkout action, branch scanning | `apt install git` / preinstalled on macOS |
| `gh` CLI | zero-config GitHub auth (`gh auth login`) | `brew install gh` / `apt install gh` — or set `token_env` instead |
| `lazygit` | `L` key (git TUI) | `brew install lazygit` · Ubuntu: [releases](https://github.com/jesseduffield/lazygit/releases) — or set `commands.git_ui` |
| `claude` or `copilot` | `a` key (AI agent) | [claude.com/claude-code](https://claude.com/claude-code) / [copilot-cli](https://github.com/github/copilot-cli) — pick via `commands.agent` |
| `xclip`, `xdg-utils` (Linux only) | `y/Y` clipboard copy · `o` open in browser | `apt install xclip xdg-utils` (macOS/Windows need nothing) |

## Configure

Config lives at `~/.config/standup/config.yaml` (override with
`$STANDUP_CONFIG`). A commented starter file is created on first run.
`${VAR}` references are expanded from the environment.

Handy commands: `standup --help` · `standup config` (path + effective
settings, tokens redacted) · `standup doctor` (checks credentials,
endpoints, tools, and font). A [JSON Schema](schema/config.schema.json)
ships in the repo — the starter file includes a `yaml-language-server`
modeline, so editors with the YAML extension autocomplete and validate
every key.

```yaml
refresh: 30s            # auto-refresh of the active tab; "off" to disable
icons: nerd             # or: ascii
commands:
  agent: claude         # AI agent for the `a` key: claude | copilot | custom
  git_ui: lazygit       # git TUI for the `L` key
# link_pattern: '[A-Z][A-Z0-9]+-\d+'   # how issue keys are found in PR titles/branches

clients:
  - name: work
    jira:
      base_url: ${WORK_JIRA_URL}
      email: ${WORK_JIRA_EMAIL}     # Cloud: email + API token
      token_env: WORK_JIRA_TOKEN
      projects: PROJ,OTHER          # optional: limit to project keys
    github:
      orgs: [your-org]              # and/or repos: [owner/repo]
    # projects_dir: ~/projects/work # local clones (default ~/projects/<name>)

  - name: selfhosted
    jira:
      base_url: https://jira.example.com
      flavor: datacenter            # self-hosted: PAT bearer auth, REST v2
      token_env: DC_JIRA_PAT
    github:
      host: github.example.com      # GitHub Enterprise works too

  - name: personal
    github: {}                      # no filter: everything involving you
```

- **Jira Cloud**: `email` + API token
  (<https://id.atlassian.com/manage-profile/security/api-tokens>).
- **Jira Data Center/Server**: `flavor: datacenter` + a Personal Access
  Token. Omitting `email` also implies datacenter.
- **Default query**: `assignee = currentUser() AND sprint in openSprints()`,
  scoped by `projects:`. On **Kanban** (no sprints) or for any custom board,
  replace it entirely with `jql:`, e.g.
  `jql: assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC`.
- **GitHub**: no config needed when logged in with the `gh` CLI (token read
  via `gh auth token`); otherwise set `token_env`.
- **`commands.agent`**: `claude` and `copilot` are built-in presets; anything
  else is run as a command line with `{prompt}` replaced by the issue context
  (appended as the last argument if the placeholder is absent).
- **`link_pattern`**: regex for extracting issue keys from PR titles and
  branch names; the default matches standard Jira keys (`ABC-123`).
- **Client privacy**: `hide_clients: true` renders only the active client in
  the header — other client names never appear on screen (number keys still
  switch). For full isolation, `standup <client>` loads only that client
  into the session. Both are handy when screen-sharing with one client.
- **`refresh`**: only the visible client is polled, never while a fetch is in
  flight; the list stays on screen during refreshes. Values under 10s are
  clamped (GitHub throttles rapid repeated searches).

## Keys

Press **`?`** anywhere for the full shortcuts menu — a which-key-style,
context-aware overlay showing every currently available key with a full
description, grouped into Navigate / Views / Item / Git & tools / Copy & open
/ System. Press any key from the menu to close it *and* run it. The menu also
includes a **legend** decoding the row markers (PR link colors, merged
state, branch markers, CI icons). The pinned
footer stays minimal: the top hints for the current row plus `? help`.

Highlights: `1-9`/`w` clients · `tab` views · `enter` detail · `→`/`←`
expand/collapse · `g` git view · `b` start work (create+push a mapping
branch for an unlinked issue, repo suggested from the title) · `C` checks ·
`A` chat · `y`/`Y` copy links · `o` browser ·
`r` refresh · `/` filter · `e` edit config · vim scrolling (`j/k`,
`ctrl+d/u/f/b`, `gg`/`G`) — and in detail views: `p` jump issue⇄PR,
`c` checkout, `L` lazygit, `t` terminal, `a` AI agent, `esc` back.

**`A` — chat pane**: converse with Claude about any issue or PR without
leaving the dashboard (from the list or a detail view). It runs your own
Claude Code headless in the item's repo with the ticket context injected,
so plain-language requests like *"add a Jira comment saying it's tested"*
or *"fix the flaky check and push"* work through your existing skills and
permission allowlists — no special commands. Tool activity streams as `⚒`
lines, each issue keeps its own resumable session, `enter` sends, `esc`
cancels a running turn or steps back. `commands.chat_permission_mode`
(default `acceptEdits`, or `plan` for read-only) governs what it may do.
The working directory resolves from the item's linked PR, a matching
branch, or the client's `repo_map` (Jira project key → repo folder); with
none of those, a type-to-filter **repo picker** asks once per issue —
enter with no filter uses the projects root as the default.

### Custom keybindings

Remap any action by id in the config:

```yaml
keys:
  git-view: G      # open linked PR with G instead of g
  filter: s
```

Action ids: `down up top bottom` · `view-toggle view-issues view-prs
client-picker` · `detail detail-alt expand collapse toggle groups-all` ·
`git-view jump checkout git-ui terminal agent chat checks start-branch` · `copy copy-linked open` ·
`refresh filter edit-config back to-list help quit`. Unknown ids and key
conflicts are reported at startup; fixed keys (digits, page scrolling) are
not remappable. Arrow keys, `ctrl+c`, and other aliases always keep working.

## Views

- **Issues** (first tab): your Jira issues in the current sprint — status
  badge colored by category, `[done/total]` subtask progress, expandable
  subtask rows — followed by GitHub issues assigned to you. Issues and
  subtasks show a ` repo#n` marker (colored by review state, with
  CI/conflict icons) when an open PR references their key, or a dim
  ` repo:branch` marker when a local/remote branch matches but no PR
  exists yet. Once the PR merges, the marker turns **purple** with the
  merge glyph and shows the **post-merge** pipeline result — the checks that
  ran on the merge commit (deploys, main-branch GHA) — so a merge that broke
  the target branch is visible right on the issue.
- **Pull Requests**: PRs you authored plus PRs where your review is
  requested, grouped GitKraken-style into action buckets: *Needs My Review,
  Waiting for Review, Ready to Merge, Resolve Conflicts, Failing CI,
  Reviewer Commented, Unassigned Reviewers, Draft*. Rows show age, CI
  status, conflicts, diffstat, and review state (approved / in review /
  changes requested).
- **Detail view**: Jira descriptions and comments rendered from wiki markup
  (Data Center) or ADF (Cloud); PR bodies and comments rendered from
  Markdown. PRs show checks & deployments, commits, and changed files;
  issues show linked PRs/branches and subtasks with their git markers.

## Releases

Every push to `main` publishes a release: the workflow bumps the patch
version automatically (`x.y.z`), or bump minor/major by including `[minor]`
or `[major]` in the commit message. Binaries are built with GoReleaser for
macOS, Linux, and Windows.

## Contributing

Contributions welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for the dev
setup, project layout, and how releases work. Bug reports: open an
[issue](https://github.com/jhonsanchez/standup/issues) with your OS,
`standup --version`, and `standup doctor` output.

## License

[MIT](LICENSE)
