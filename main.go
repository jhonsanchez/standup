package main

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jhonsanchez/standup/internal/config"
	"github.com/jhonsanchez/standup/internal/ui"
)

// version is stamped by GoReleaser at release time.
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("standup", version)
		return
	}
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
			fmt.Fprintf(os.Stderr, "standup: no client named %q in config\n", name)
			os.Exit(1)
		}
	}

	p := tea.NewProgram(ui.New(cfg), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "standup:", err)
		os.Exit(1)
	}
}
