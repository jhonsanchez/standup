package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jhonsanchez/standup/internal/config"
	"github.com/jhonsanchez/standup/internal/github"
	"github.com/jhonsanchez/standup/internal/gitscan"
	"github.com/jhonsanchez/standup/internal/jira"
	"github.com/jhonsanchez/standup/internal/ui"
)

func printHelp() {
	fmt.Printf(`standup — Jira + GitHub sprint dashboard in your terminal

Usage:
  standup              launch the TUI with all configured clients
  standup <client>     launch with a single client only (screen-share safe)
  standup config       print the config path and effective settings (tokens redacted)
  standup doctor       check config, credentials, endpoints, and tools
  standup upgrade      update to the latest release (alias: update)
  standup --version    print version
  standup --help       this help

Config:  %s
         (override with $STANDUP_CONFIG; a commented starter file is
         created on first run)
Docs:    https://github.com/jhonsanchez/standup
`, config.Path())
}

func loadOrExit() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		var created config.ErrCreated
		if errors.As(err, &created) {
			fmt.Println(err.Error())
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "standup:", err)
		os.Exit(1)
	}
	return cfg
}

// cmdConfig prints the config path and the effective (env-expanded) settings
// with secrets redacted.
func cmdConfig() {
	fmt.Println("config:", config.Path())
	cfg := loadOrExit()
	for i := range cfg.Clients {
		if j := cfg.Clients[i].Jira; j != nil && j.Token != "" {
			j.Token = "<redacted>"
		}
		if g := cfg.Clients[i].GitHub; g != nil && g.Token != "" {
			g.Token = "<redacted>"
		}
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "standup:", err)
		os.Exit(1)
	}
	fmt.Println("\n# effective configuration (defaults applied at runtime)")
	fmt.Print(string(out))
	fmt.Printf("# refresh interval: %s · icons: %s · agent: %s · git UI: %s\n",
		cfg.RefreshInterval(), orDefault(cfg.Icons, "nerd"),
		cfg.AgentCommand("…")[0], cfg.GitUICommand())
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// cmdDoctor checks every client's credentials and endpoints plus local tools.
func cmdDoctor() {
	cfg := loadOrExit()
	fmt.Println("config:", config.Path(), "✓")
	failed := false
	fail := func(format string, args ...any) {
		failed = true
		fmt.Printf("  ✗ "+format+"\n", args...)
	}
	ok := func(format string, args ...any) {
		fmt.Printf("  ✓ "+format+"\n", args...)
	}

	for _, c := range cfg.Clients {
		fmt.Printf("\nclient %s:\n", c.Name)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if c.Jira != nil {
			if err := jira.Ping(ctx, c.Jira); err != nil {
				fail("jira %s — %v", c.Jira.BaseURL, err)
			} else {
				flavor := "cloud"
				if c.Jira.IsDataCenter() {
					flavor = "datacenter"
				}
				ok("jira %s (%s) authenticated", c.Jira.BaseURL, flavor)
			}
		}
		if c.GitHub != nil {
			if login, err := github.Ping(ctx, c.GitHub); err != nil {
				fail("github — %v", err)
			} else {
				ok("github authenticated as %s", login)
			}
		}
		base := c.ProjectsBase()
		if st, err := os.Stat(base); err == nil && st.IsDir() {
			ok("projects_dir %s (%d repos)", base, countRepos(base))
		} else {
			fmt.Printf("  · projects_dir %s does not exist (checkout/lazygit/agent keys disabled)\n", base)
		}
		cancel()
	}

	fmt.Println("\ntools:")
	for _, tool := range []string{cfg.AgentCommand("…")[0], cfg.GitUICommand(), "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			fail("%s not found in PATH", tool)
		} else {
			ok("%s", tool)
		}
	}
	if cfg.Icons != "ascii" {
		if ui.NerdFontDetected() {
			ok("nerd font detected")
		} else {
			fail("no Nerd Font detected — install JetBrainsMono Nerd Font or set icons: ascii")
		}
	}

	if failed {
		fmt.Println("\nsome checks failed")
		os.Exit(1)
	}
	fmt.Println("\nall checks passed")
}

func countRepos(base string) int {
	return len(gitscanRepos(base))
}

func gitscanRepos(base string) map[string]bool {
	repos := map[string]bool{}
	for _, b := range gitscan.Scan(base) {
		repos[b.Repo] = true
	}
	return repos
}
