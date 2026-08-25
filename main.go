package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jhonsanchez/standup/internal/config"
	"github.com/jhonsanchez/standup/internal/ui"
	"github.com/jhonsanchez/standup/internal/upgrade"
)

// version is stamped by GoReleaser at release time.
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v", "version":
			fmt.Println("standup", version)
			return
		case "--help", "-h", "help":
			printHelp()
			return
		case "config":
			cmdConfig()
			return
		case "doctor":
			cmdDoctor()
			return
		case "upgrade":
			if err := upgrade.Run(version); err != nil {
				fmt.Fprintln(os.Stderr, "standup: upgrade failed:", err)
				os.Exit(1)
			}
			return
		}
		if strings.HasPrefix(os.Args[1], "-") {
			fmt.Fprintf(os.Stderr, "standup: unknown flag %q — see `standup --help`\n", os.Args[1])
			os.Exit(1)
		}
	}

	cfg := loadOrExit()
	// `standup <client>` loads only that client — the others never enter the
	// session (safe for screen sharing).
	if len(os.Args) > 1 {
		name := os.Args[1]
		found := false
		for _, c := range cfg.Clients {
			if c.Name == name {
				cfg.Clients = []config.Client{c}
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "standup: no client named %q in config — see `standup --help`\n", name)
			os.Exit(1)
		}
	}

	p := tea.NewProgram(ui.New(cfg), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "standup:", err)
		os.Exit(1)
	}
}
