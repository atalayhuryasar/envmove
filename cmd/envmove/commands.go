package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/atalayhuryasar/envmove/internal/config"
	"github.com/atalayhuryasar/envmove/internal/gitx"
	"github.com/atalayhuryasar/envmove/internal/syncer"
)

// cmdRestore puts the project state back to how it was at an earlier point.
//
// This is the safety net: not a sync feature, a way back from having broken something.
func cmdRestore(args []string) error {
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

	ref := ""
	force := false
	for _, a := range args {
		switch {
		case a == "--force" || a == "-f":
			force = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag: %s", a)
		default:
			ref = a
		}
	}

	if ref == "" {
		points, err := s.RestorePoints(10)
		if err != nil {
			return err
		}
		if len(points) == 0 {
			return fmt.Errorf("there are no snapshots to restore")
		}
		// The newest entry is the state that is already on disk. Marking it is the
		// difference between "I broke something and want the one before" and quietly
		// restoring the broken state the caller was trying to escape.
		fmt.Println("snapshots, newest first:")
		for i, p := range points {
			marker := ""
			if i == 0 {
				marker = "   ← current state"
			}
			fmt.Printf("  %d) %s  %s  %s%s\n", i+1, p.When[:16], p.Commit[:7], p.Message, marker)
		}
		if len(points) > 1 {
			fmt.Println("\n  to go back, pick 2 or later")
		}
		fmt.Print("  number to restore, or 'n' to cancel: ")
		choice := strings.TrimSpace(readLine())
		if choice == "" || normalise(choice) == "n" {
			return nil
		}
		var idx int
		if _, err := fmt.Sscanf(choice, "%d", &idx); err != nil || idx < 1 || idx > len(points) {
			return fmt.Errorf("invalid selection")
		}
		ref = points[idx-1].Commit
	}

	commit, err := s.ResolveRef(ref)
	if err != nil {
		return err
	}
	written, err := s.Restore(commit, force)
	if err != nil {
		if len(written) > 0 || strings.Contains(err.Error(), "ezecek") {
			fmt.Fprintln(os.Stderr, "")
		}
		return err
	}

	fmt.Printf("✓ restored to %s\n", commit[:7])
	for _, w := range written {
		fmt.Printf("  ↓ %s\n", strings.TrimPrefix(w, "− "))
	}
	fmt.Println("\n  This is a local restore. Pushing it (envmove) makes it permanent,")
	fmt.Println("  which means the state you just broke is gone.")
	return nil
}

// cmdRotateKey replaces this machine's key, and optionally the whole recipient list.
func cmdRotateKey(args []string) error {
	all := false
	for _, a := range args {
		switch a {
		case "--all":
			all = true
		case "--help", "-h":
			fmt.Print(`envmove rotate-key [--all]

      Issues a new private key for this machine. The old one can no longer read
      anything written from now on.

  --all  Replaces every recipient, including other machines. Use this when a key
         was compromised: leaving the old key as a recipient would let it read
         everything written after this point.

  Note: snapshots already in git history stay encrypted with the keys they were
  written with. Rotating protects the future, not the past.
`)
			return nil
		default:
			return fmt.Errorf("unknown flag: %s", a)
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
	fmt.Println("This machine's key is about to be replaced.")
	s = s.WithIdentity(holderRecipient(repo), func(identity string) error {
		if err := saveKey(repo.Root, identity); err != nil {
			return err
		}
		return rewrapRecovery(repo, identity)
	})

	res, err := s.Rotate(syncer.RotateOptions{AllRecipients: all})
	if err != nil {
		return err
	}
	// Rotate only rewrites the in-memory recipient list. Without persisting it the new
	// key would exist in the keychain and nowhere else, and every machine except this
	// one would be reading config that names a key they do not have.
	if err := config.Save(repo.Root, cfg); err != nil {
		return err
	}

	fmt.Printf("✓ yeni anahtar: %s…\n", short(res.NewRecipient))
	if len(res.Dropped) > 0 {
		fmt.Printf("  dropped recipient: %s\n", strings.Join(shortenAll(res.Dropped), ", "))
	}
	if len(res.Kept) > 1 {
		fmt.Printf("  kept recipient: %s\n", strings.Join(shortenAll(res.Kept), ", "))
	}
	if res.Reencrypted {
		fmt.Println("  the current snapshot was re-encrypted")
	}
	if configIsDirty(repo.Root) {
		if askYesNo("publish the new recipient list") {
			if err := publishConfig(repo); err != nil {
				fmt.Fprintf(os.Stderr, "⚠ could not publish: %v\n", err)
				fmt.Println("  by hand: git add envmove.toml && git commit -m 'envmove' && git push")
			}
		} else {
			fmt.Println("  ⚠ not published. The other machines still expect the old key.")
			fmt.Println("    git add envmove.toml && git commit -m 'envmove' && git push")
		}
	}

	fmt.Println("\n⚠ snapshots already in history stay encrypted with the old key.")
	if all {
		fmt.Println("  If the old key was compromised, those snapshots are still readable.")
	} else {
		fmt.Println("  Before handing this machine to someone else, deal with the dropped key.")
	}
	return nil
}

// publishConfig commits and pushes envmove.toml.
//
// It stages only that one file and never touches anything else in the working tree.
// A sync tool that swept up unrelated changes in a commit of its own would be a menace.
func publishConfig(repo *gitx.Repo) error {
	path := config.Path(repo.Root)
	rel := config.FileName

	if err := repo.StageOnly(rel); err != nil {
		return err
	}
	if _, err := repo.CommitPath(
		fmt.Sprintf("envmove: config published (%d recipients)", recipientCount(repo)), rel); err != nil {
		return err
	}
	if repo.HasRemote(syncer.Remote) {
		if err := repo.PushBranch(syncer.Remote); err != nil {
			return err
		}
	}
	fmt.Printf("✓ %s published\n", rel)
	_ = path
	return nil
}

func recipientCount(repo *gitx.Repo) int {
	cfg, err := config.Load(repo.Root)
	if err != nil {
		return 0
	}
	return len(cfg.Recipients)
}

func shortenAll(keys []string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = short(k)
	}
	return out
}
