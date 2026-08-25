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
		fmt.Fprintln(os.Stderr, "launchpad:", err)
		os.Exit(1)
	}

	p := tea.NewProgram(ui.New(cfg), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "launchpad:", err)
		os.Exit(1)
	}
}
