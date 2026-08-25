# Contributing to standup

Thanks for your interest! standup is MIT-licensed and contributions of all
kinds are welcome — bug reports, docs fixes, features.

## Development setup

Prerequisites: Go 1.22+ (no other build dependencies).

```sh
git clone https://github.com/jhonsanchez/standup
cd standup
go build -o standup .
```

Run against a scratch config so you don't touch your real one:

```sh
STANDUP_CONFIG=/tmp/standup-dev.yaml ./standup   # first run writes a starter file
```

`standup doctor` is useful while developing auth/config changes.

## Project layout

| Path | What lives there |
| --- | --- |
| `main.go`, `cmds.go` | CLI entry, subcommands (`config`, `doctor`, `upgrade`, help) |
| `internal/config` | YAML config, env expansion, defaults, paths |
| `internal/jira` | Jira Cloud (REST v3) + Data Center (REST v2) clients |
| `internal/github` | GitHub GraphQL (PRs, detail) + REST (issue search) |
| `internal/jirafmt` | Renders Jira wiki markup, ADF, and GitHub Markdown to styled terminal text |
| `internal/gitscan` | Local clone/branch discovery |
| `internal/ui` | The Bubble Tea TUI: list, detail stack, keys, styles |
| `internal/upgrade` | `standup upgrade` self-update |
| `schema/` | JSON Schema for the config (keep in sync with `internal/config`) |

## Making changes

- `gofmt` and `go vet ./...` must pass — CI runs both before releasing.
- Match the existing style; keep the UI's help bar in sync when you add or
  change keybindings (it's context-sensitive — see `listHelp`/`detailHelp`).
- If you add a config key: document it in `internal/config` (struct comment),
  the starter config, `schema/config.schema.json`, and the README.
- No test suite yet — verify against a real Jira/GitHub account and describe
  what you tested in the PR.

## Pull requests

1. Fork, branch from `main`, make your change.
2. Open a PR describing the what and why; screenshots/casts help for UI work.

## Releases

Every merge to `main` publishes a release automatically: patch bump by
default; include `[minor]` or `[major]` in the merge commit message to bump
those instead. GoReleaser builds macOS/Linux/Windows binaries and updates
the Homebrew tap.

## Reporting bugs

Open a [GitHub issue](https://github.com/jhonsanchez/standup/issues) with
your OS, terminal, `standup --version`, and `standup doctor` output
(redact anything sensitive).
