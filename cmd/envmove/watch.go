package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/atalayhuryasar/envmove/internal/config"
	"github.com/atalayhuryasar/envmove/internal/gitx"
	"github.com/atalayhuryasar/envmove/internal/syncer"
)

// cmdWatch publishes carried files once they stop changing.
//
// This exists because of a hole in the promise. Every other path is covered: a commit
// runs a hook, a push runs a hook, an agent session runs a hook. But editing a gitignored
// file and then running no git command at all triggers nothing, because git bails out
// with "nothing to commit" before any hook is reached. The state simply sat there.
//
// It is opt-in rather than started by setup. A daemon that runs for days on a
// contributor's machine is a thing you should choose, not something a setup script
// installs behind your back.
func cmdWatch(args []string) error {
	quiet := 60 * time.Second
	every := 5 * time.Second

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--quiet":
			quiet = time.Minute
			i++
			if i < len(args) && isDuration(args[i]) {
				if d, ok := parseDuration(args[i]); ok {
					quiet = d
				}
				i++
			}
		case "--help", "-h":
			fmt.Print(`envmove watch [--quiet 90s]

  Publishes carried files once they have been untouched for a while, then repeats.

    --quiet   how long a file must be still before it is published (default 60s)

  Why the delay: an agent rewrites .env several times in a row. Publishing on every
  write would push a half-finished file. A file is only published after it has been
  completely quiet, so partial states never reach the remote.

  Runs until interrupted. Safe to leave in a spare terminal or a tmux pane.
`)
			return nil
		default:
			return fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	repo, err := gitx.Open()
	if err != nil {
		return err
	}
	if !config.Exists(repo.Root) {
		return fmt.Errorf("this repository is not set up — run `envmove setup`")
	}
	cfg, err := config.Load(repo.Root)
	if err != nil {
		return err
	}
	s, err := buildSyncer(repo, cfg)
	if err != nil {
		return err
	}

	// Bring this machine level before watching for changes, so the first quiet period
	// starts from a known state rather than from whatever the last run left behind.
	if _, written, err := s.Pull(); err == nil {
		for _, w := range written {
			fmt.Printf("↓ %s\n", strings.TrimPrefix(w, "− "))
		}
	}

	fmt.Printf("watching %d files; publishing after %s of quiet. Ctrl-C to stop.\n",
		len(cfg.Paths), quiet)

	stop := make(chan struct{})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		fmt.Println("\nstopping; one last sync")
		_, _, _ = s.Sync()
		close(stop)
	}()

	return s.Watch(syncer.WatchOptions{
		Quiet: quiet,
		Every: every,
		OnChange: func(line string) {
			fmt.Printf("%s  %s\n", time.Now().Format("15:04:05"), line)
		},
		OnError: func(err error) {
			fmt.Printf("%s  could not sync: %v\n", time.Now().Format("15:04:05"), err)
		},
	}, stop)
}

func isDuration(s string) bool {
	_, ok := parseDuration(s)
	return ok
}

func parseDuration(s string) (time.Duration, bool) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, false
	}
	if d <= 0 {
		return 0, false
	}
	return d, true
}
