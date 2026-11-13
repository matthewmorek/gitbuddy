package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/matthewmorek/gitbuddy/internal/buildinfo"
	"github.com/matthewmorek/gitbuddy/internal/gitrepo"
	"github.com/matthewmorek/gitbuddy/internal/palette"
	"github.com/matthewmorek/gitbuddy/internal/preferences"
	"github.com/matthewmorek/gitbuddy/internal/ui"
)

func run() error {
	args := os.Args[1:]
	path := "."
	if len(args) > 1 {
		return fmt.Errorf("usage: gitbuddy [repository] | --help | --version | --licenses")
	}
	if len(args) == 1 {
		switch args[0] {
		case "--version":
			fmt.Println("gitbuddy " + buildinfo.Version())
			return nil
		case "--licenses":
			fmt.Print(buildinfo.Notices)
			return nil
		case "--help", "-h":
			fmt.Println("gitbuddy [repository]\n\nKeyboard-first Git history. Ctrl+P opens commands and settings.\n\n--version   Print version\n--licenses  Print embedded license notices\n--help      Show help")
			return nil
		default:
			if len(args[0]) > 0 && args[0][0] == '-' {
				return fmt.Errorf("unknown option: %s", gitrepo.Safe(args[0]))
			}
			path = args[0]
		}
	}
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return fmt.Errorf("run Gitbuddy in an interactive terminal")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo, err := gitrepo.Open(ctx, path)
	if err != nil {
		return err
	}
	settings := preferences.Load("")
	themes := palette.Load(settings.Directory)
	program := tea.NewProgram(ui.New(ctx, repo, settings, themes), tea.WithoutSignalHandler())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-signals:
				program.Send(ui.QuitMsg{})
			case <-done:
				return
			}
		}
	}()
	_, err = program.Run()
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gitbuddy:", gitrepo.Safe(err.Error()))
		os.Exit(1)
	}
}
