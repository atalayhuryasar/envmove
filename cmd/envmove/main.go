// Command envmove carries the project context that git refuses to carry.
//
// Typical use is no use at all: `envmove setup` once per machine, after which git and
// agent hooks keep everything in step. The remaining commands exist for the moments
// when something needs to be seen or changed by hand.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/atalayhuryasar/envmove/internal/agent"
	"github.com/atalayhuryasar/envmove/internal/config"
	"github.com/atalayhuryasar/envmove/internal/context"
	"github.com/atalayhuryasar/envmove/internal/crypt"
	"github.com/atalayhuryasar/envmove/internal/detect"
	"github.com/atalayhuryasar/envmove/internal/gitx"
	"github.com/atalayhuryasar/envmove/internal/keystore"
	"github.com/atalayhuryasar/envmove/internal/syncer"
)

// version is overwritten at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// envmove's own push runs through git, which fires the repository's pre-push
	// hook, which would call envmove again. This marker breaks that loop.
	if os.Getenv("ENVMOVE_INTERNAL") != "" {
		return
	}

	args := os.Args[1:]

	// Flags are handled before the command is chosen. Without this, `envmove --help`
	// fell through to the default command and tried to sync, which is a baffling thing
	// to happen when someone asks for help.
	for _, a := range args {
		switch a {
		case "-h", "--help", "help":
			usage()
			return
		case "-v", "--version":
			fmt.Println("envmove", version)
			return
		}
		if !strings.HasPrefix(a, "-") {
			break // reached the subcommand, stop scanning flags
		}
	}

	cmd := "sync"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}

	var err error
	switch cmd {
	case "setup":
		err = cmdSetup(args)
	case "sync", "push", "pull":
		err = cmdSync(cmd, args)
	case "add":
		err = cmdAdd(args)
	case "doctor":
		err = cmdDoctor(args)
	case "recover":
		err = cmdRecover(args)
	case "restore":
		err = cmdRestore(args)
	case "rotate-key":
		err = cmdRotateKey(args)
	case "hook":
		event := "session-start"
		if len(args) > 0 {
			event = args[0]
		}
		err = cmdHook(event)
	case "version":
		fmt.Println("envmove", version)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		if errors.Is(err, ErrBlocked) {
			fmt.Fprintln(os.Stderr, "\n✗ cannot continue while conflicts are unresolved")
			fmt.Fprintln(os.Stderr, "  `envmove doctor` shows which files are affected")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "\n✗", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`envmove — the layer git will not carry

  envmove setup          set this machine up (once)
  envmove                sync
  envmove add X          carry another file
  envmove doctor         explain what is wrong
  envmove recover        restore the key if the keychain is gone
  envmove restore [2h|1d|commit]   put the project back
  envmove rotate-key [--all]       replace the key
  envmove version

In daily use you do not run envmove. Git and agent hooks do it.
`)
}

// openStore returns the key store for this machine, or a clear reason it is not usable.
// Every key read and write goes through here rather than calling the keychain directly,
// so the platform-specific part of envmove is one function.
func openStore() (keystore.Store, error) {
	return keystore.Open()
}

func loadKey(repoRoot string) ([]byte, bool, error) {
	store, err := openStore()
	if err != nil {
		return nil, false, err
	}
	return keystore.LoadFrom(repoRoot, store, keystore.Account(repoRoot))
}

func saveKey(repoRoot string, identity string) error {
	store, err := openStore()
	if err != nil {
		return err
	}
	usedFallback, err := keystore.SaveWithFallback(repoRoot, store, keystore.Account(repoRoot), []byte(identity))
	if usedFallback {
		fmt.Fprintf(os.Stderr, "⚠ %s is unreachable, the key was written to %s (0600)\n", store.Name(), keystore.FallbackPath(repoRoot))
	}
	return err
}

// ---------------------------------------------------------------- setup

func cmdSetup(args []string) error {
	repo, err := gitx.Open()
	if err != nil {
		return err
	}
	fmt.Printf("repo: %s\n", repo.Root)

	cfg := &config.Config{Branch: config.DefaultBranch}
	if config.Exists(repo.Root) {
		existing, err := config.Load(repo.Root)
		if err != nil {
			return err
		}
		cfg = existing
		fmt.Printf("found an existing config (branch: %s)\n", cfg.Branch)
	}

	if !askYesNo("set this repository up?") {
		fmt.Println("iptal edildi.")
		return nil
	}

	if cfg.Branch == config.DefaultBranch {
		fmt.Print("branch name (everyone should use their own): ")
		if name := readLine(); name != "" {
			cfg.Branch = name
		}
	}

	candidates, err := detect.Scan(repo)
	if err != nil {
		return err
	}
	selected, err := confirmPaths(candidates)
	if err != nil {
		return err
	}
	cfg.Paths = mergePaths(cfg.Paths, selected)

	id, err := loadOrCreateIdentity(repo.Root)
	if err != nil {
		return err
	}
	if cfg.AddRecipient(id.Recipient().String()) {
		fmt.Printf("+ this machine's public key added: %s…\n", short(id.Recipient().String()))
	}

	// The keychain is the only place the private key normally lives, which makes it a
	// single point of failure. A passphrase-wrapped copy is written before anything
	// else, so that losing the machine never means losing the secrets.
	if !keystore.RecoveryExists(repo.Root) {
		if err := writeRecovery(repo.Root, id); err != nil {
			fmt.Fprintf(os.Stderr, "\n✗ recovery copy could not be written: %v\n", err)
			fmt.Fprintln(os.Stderr, "  The key would live only in the keychain, and losing it means losing the data.")
			fmt.Fprintln(os.Stderr, "  You need to provide a recovery passphrase to finish.")
			return err
		}
	}

	if err := config.Save(repo.Root, cfg); err != nil {
		return err
	}
	fmt.Printf("✓ %s written (%d files)\n", config.FileName, len(cfg.Paths))

	// The config is the contract both machines share, and it now names this machine's
	// public key. Until it is published the other machines cannot re-encrypt for us and
	// will not be able to read what we push. Forgetting this step was the single most
	// likely way to end up stuck, so it is offered rather than described.
	if configIsDirty(repo.Root) {
		fmt.Println("\nThe config has to be published so the other machines know this one exists.")
		if askYesNo("commit and push it now") {
			if err := publishConfig(repo); err != nil {
				fmt.Fprintf(os.Stderr, "⚠ could not publish: %v\n", err)
				fmt.Printf("  by hand: git add %s && git commit -m 'envmove' && git push\n", config.FileName)
			}
		} else {
			fmt.Printf("  Later: git add %s && git commit -m 'envmove' && git push\n", config.FileName)
		}
	}

	warnIfPublicRemote(repo, cfg)

	if err := installHooks(repo); err != nil {
		return err
	}

	if _, err := buildSyncer(repo, cfg); err != nil {
		return err
	}
	fmt.Println("✓ git hooks installed (post-commit, pre-push)")
	installAgentHooks(repo)

	if result, changed, err := refreshExample(repo); err == nil {
		reportExample(result, changed, false)
	}

	fmt.Printf("\nDone. %d files will travel encrypted on the %q branch.\n", len(cfg.Paths), cfg.Branch)
	fmt.Println("Nothing else to do — git and agent hooks take it from here.")
	fmt.Println("To see the state:  envmove doctor")
	return nil
}

func confirmPaths(candidates []detect.Candidate) ([]string, error) {
	if len(candidates) == 0 {
		fmt.Println("no files found worth sharing.")
		return nil, nil
	}
	fmt.Println("\nfiles detected:")
	for _, c := range candidates {
		fmt.Printf("  %-40s %s\n", c.Path, c.Reason)
	}
	fmt.Println("\n[all]  include all   [one]  choose one by one   [none]  skip everything")
	// The answer is matched loosely on purpose. A prompt that rejects what someone
	// naturally types is worse than one that accepts a synonym.
	switch normalise(readLine()) {
	case "", "all", "a", "everything":
		var out []string
		for _, c := range candidates {
			if c.Class == detect.Sync {
				out = append(out, c.Path)
			}
		}
		return out, nil
	case "one", "1":
		var out []string
		for _, c := range candidates {
			if c.Class != detect.Sync {
				continue
			}
			if askYesNo("  " + c.Path) {
				out = append(out, c.Path)
			}
		}
		return out, nil
	default:
		return nil, nil
	}
}

func loadOrCreateIdentity(repoRoot string) (*age.X25519Identity, error) {
	if stored, ok, err := loadKey(repoRoot); err == nil && ok {
		ids, err := age.ParseIdentities(strings.NewReader(string(stored)))
		if err == nil && len(ids) > 0 {
			if id, ok := ids[0].(*age.X25519Identity); ok {
				return id, nil
			}
		}
	}

	id, err := crypt.GenerateIdentity()
	if err != nil {
		return nil, err
	}
	if err := saveKey(repoRoot, id.String()); err != nil {
		return nil, err
	}
	return id, nil
}

// selfPath is the absolute path of the running binary, baked into the git hooks.
// Hooks cannot rely on PATH: GUI git clients (GitHub Desktop, some IDEs) launch git
// with a minimal environment where /usr/local/bin and friends are missing, and a hook
// that cannot find envmove would silently block every push.
func selfPath() string {
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			return resolved
		}
		return exe
	}
	return "envmove"
}

// configIsDirty reports whether envmove.toml has uncommitted changes, which almost
// always means a new machine has not announced itself to the other side yet.
func configIsDirty(root string) bool {
	out, err := exec.Command("git", "-C", root, "status", "--porcelain", config.FileName).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// writeRecovery stores a passphrase-wrapped copy of the private key and shows the
// passphrase exactly once. The default is a generated one, because a passphrase the
// user invents under no pressure is usually a weak one.
func writeRecovery(repoRoot string, id *age.X25519Identity) error {
	var passphrase string
	if askYesNo("\ngenerate a recovery passphrase for you? (you can pick your own)") {
		generated, err := crypt.GeneratePassphrase()
		if err != nil {
			return err
		}
		passphrase = generated
		fmt.Printf("\n\033[1m  RECOVERY PASSPHRASE:  %s\033[0m\n", generated)
		fmt.Println("  Write this down. It will not be shown again.")
		fmt.Println("  (the dashes are part of the passphrase — copy and paste it)")
		fmt.Println("  1Password, a password manager or paper — 1Password is the obvious choice.")
	} else {
		passphrase = readPassphrase()
	}
	if passphrase == "" {
		return fmt.Errorf("no recovery passphrase was provided")
	}

	wrapped, err := crypt.WrapWithPassphrase(passphrase, []byte(id.String()))
	if err != nil {
		return err
	}
	if err := keystore.WriteRecovery(repoRoot, wrapped); err != nil {
		return err
	}
	fmt.Printf("✓ recovery copy written (%s)\n", keystore.RecoveryPath(repoRoot))
	return nil
}

// cmdRecover restores the private key from the passphrase-wrapped copy and puts it
// back in the keychain. It is the path taken when the keychain is gone: a new machine,
// a reinstalled macOS, a lost login.
func cmdRecover(args []string) error {
	repo, err := gitx.Open()
	if err != nil {
		return err
	}
	wrapped, ok := keystore.ReadRecovery(repo.Root)
	if !ok {
		return fmt.Errorf("there is no recovery copy for this repository (%s)", keystore.RecoveryPath(repo.Root))
	}

	fmt.Print("recovery passphrase: ")
	passphrase := strings.TrimSpace(readLine())
	if passphrase == "" {
		return fmt.Errorf("no passphrase entered")
	}

	plain, err := crypt.UnwrapWithPassphrase(keystore.TrimPassphrase(passphrase), wrapped)
	if err != nil {
		return err
	}
	ids, err := age.ParseIdentities(strings.NewReader(string(plain)))
	if err != nil || len(ids) == 0 {
		return fmt.Errorf("the recovery copy could not be read")
	}
	if err := saveKey(repo.Root, string(plain)); err != nil {
		return fmt.Errorf("could not write to the keychain: %w", err)
	}
	fmt.Println("✓ key restored to the keychain")
	fmt.Println("  `envmove` diyerek senkronlamaya devam edebilirsin.")
	return nil
}

// cmdHook is what an AI coding tool calls at session boundaries.
//
// It never fails the session. A sync problem is reported and the agent carries on,
// because an agent that refuses to start is worse than an agent that starts with
// slightly stale context.
func cmdHook(event string) error {
	if event != "session-start" && event != "session-end" {
		return fmt.Errorf("bilinmeyen hook: %s", event)
	}

	repo, err := gitx.Open()
	if err != nil {
		return nil
	}
	if !config.Exists(repo.Root) {
		return nil
	}
	cfg, err := config.Load(repo.Root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "envmove: %v\n", err)
		return nil
	}
	s, err := buildSyncer(repo, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "envmove: %v\n", err)
		return nil
	}

	if event == "session-end" {
		if _, _, err := s.Push(); err != nil {
			fmt.Fprintf(os.Stderr, "envmove: %v\n", err)
		}
		return nil
	}

	// Session start: get current first, then tell the agent what it is looking at.
	d, written, err := s.Pull()
	if err != nil {
		fmt.Fprintf(os.Stderr, "envmove: %v\n", err)
	}
	fmt.Print(context.Brief(repo.Root, cfg, d, written).Markdown(time.Now()))
	return nil
}

// installAgentHooks wires envmove into whichever AI tools this repo uses.
func installAgentHooks(repo *gitx.Repo) {
	kinds := agent.Detect(repo.Root)
	if len(kinds) == 0 {
		fmt.Println("ℹ no AI tool folder found (.claude/ .cursor/ AGENTS.md)")
		fmt.Println("  You can wire it up by hand: envmove hook session-start")
		return
	}
	for _, kind := range kinds {
		path, err := agent.Install(repo.Root, kind)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠ %s: %v\n", kind, err)
			continue
		}
		fmt.Printf("✓ %s wired up → %s\n", kind, relativeTo(repo.Root, path))
	}
}

func relativeTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return rel
	}
	return path
}

// readPassphrase asks in a loop.
//
// A single typo used to end setup with the key in the keychain and no recovery copy,
// which is the one state this whole mechanism exists to prevent, and it happened
// quietly enough that setup still reported success.
func readPassphrase() string {
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			fmt.Printf("  tekrar dene (%d/3): ", attempt)
		} else {
			fmt.Print("  recovery passphrase: ")
		}
		passphrase := strings.TrimSpace(readLine())
		if len(passphrase) >= 12 {
			return keystore.TrimPassphrase(passphrase)
		}
		if passphrase == "" {
			fmt.Println("  leaving this empty means no recovery copy.")
			continue
		}
		fmt.Printf("  too short (%d characters), it needs at least 12.\n", len(passphrase))
	}
	return ""
}

func installHooks(repo *gitx.Repo) error {
	selfPath := selfPath()
	dir := filepath.Join(repo.Root, ".git", "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	scripts := map[string]string{
		// post-commit: keep code and context travelling together.
		"post-commit": "#!/bin/sh\nexec " + selfPath + " sync --hook --quiet\n",
		// pre-push: never push code while context is left behind. ENVMOVE_NO_PUSH
		// stops the hook's own sync from recursing into git push.
		"pre-push": "#!/bin/sh\nENVMOVE_NO_PUSH=1 exec " + selfPath + " sync --hook --quiet\n",
	}
	for name, body := range scripts {
		path := filepath.Join(dir, name)
		if existing, err := os.ReadFile(path); err == nil && !strings.Contains(string(existing), "envmove") {
			fmt.Printf("⚠ a %s hook already exists, left alone\n", name)
			continue
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func warnIfPublicRemote(repo *gitx.Repo, cfg *config.Config) {
	url := repo.RemoteURL(syncer.Remote)
	if url == "" {
		fmt.Println("\nℹ no remote — state will stay on this machine")
		return
	}
	if strings.Contains(url, "github.com") && !cfg.PublicAcknowledged {
		fmt.Printf(`
WARNING: this repository is on github.com and may be public.

   The encrypted files go there PERMANENTLY. If the key is ever compromised,
   everything in them at that moment is exposed for good.

   Do not put production secrets here. Use 1Password or Doppler.

`)
		if askYesNo("   I understand the risk, continue?") {
			cfg.PublicAcknowledged = true
		}
	}
}

// ---------------------------------------------------------------- sync

func cmdSync(cmd string, args []string) error {
	quiet := hasFlag(args, "--quiet")
	inHook := hasFlag(args, "--hook")

	repo, err := gitx.Open()
	if err != nil {
		return err
	}
	if !config.Exists(repo.Root) {
		if inHook {
			return nil
		}
		return fmt.Errorf("this repository is not set up — run `envmove setup`")
	}
	cfg, err := config.Load(repo.Root)
	if err != nil {
		return fail(err, inHook)
	}
	s, err := buildSyncer(repo, cfg)
	if err != nil {
		return fail(err, inHook)
	}

	if cmd != "push" {
		d, written, err := s.Pull()
		if err != nil {
			return fail(err, inHook)
		}
		if !quiet {
			for _, w := range written {
				fmt.Printf("↓ %s\n", w)
			}
		}
		reportConflicts(d, quiet)
		if len(d.Conflicts) > 0 {
			return ErrBlocked
		}
	}

	if cmd == "pull" {
		return nil
	}

	d, published, err := s.Push()
	if err != nil {
		return fail(err, inHook)
	}
	if !quiet {
		for _, c := range d.Changes {
			switch c.Kind {
			case syncer.Outgoing:
				fmt.Printf("↑ %s\n", c.Path)
			case syncer.SameState:
			default:
				fmt.Printf("· %s (%s)\n", c.Path, c.Kind)
			}
		}
	}
	if published && !quiet {
		fmt.Printf("\n✓ pushed to the %q branch\n", cfg.Branch)
	}
	if result, changed, err := refreshExample(repo); err == nil {
		reportExample(result, changed, quiet)
	}
	return nil
}

// ErrBlocked is the only sync failure that stops a code push. It is raised when files
// disagree, because pushing over one side would quietly destroy work.
var ErrBlocked = errors.New("blocked by conflicts")

// fail decides whether a sync problem is worth stopping a commit or a push for.
//
// Only an unresolvable disagreement is worth blocking. A network hiccup, a missing key
// or a machine that has not yet published its config will all clear themselves on the
// next run, and none of them are a good reason to make it impossible to ship code.
func fail(err error, inHook bool) error {
	if inHook && !errors.Is(err, syncer.ErrConflicts) {
		fmt.Fprintf(os.Stderr, "envmove: %v\n  (the code was pushed; state will settle on the next run)\n", err)
		return nil
	}
	return err
}

func reportConflicts(d *syncer.Diff, quiet bool) {
	if len(d.Conflicts) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\n⚠ %d files changed on both sides and were left alone:\n", len(d.Conflicts))
	for _, p := range d.Conflicts {
		fmt.Fprintf(os.Stderr, "  %s\n", p)
	}
	if !quiet {
		fmt.Fprintln(os.Stderr, "\n  decide which one to keep, then run this again.")
	}
}

// ---------------------------------------------------------------- add

func cmdAdd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: envmove add <path>")
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

	added := make([]string, 0, len(args))
	for _, arg := range args {
		abs, err := filepath.Abs(arg)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repo.Root, abs)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if contains(cfg.Paths, rel) {
			fmt.Printf("· %s zaten listede\n", rel)
			continue
		}
		cfg.Paths = append(cfg.Paths, rel)
		added = append(added, rel)
	}
	if len(added) == 0 {
		return nil
	}
	sort.Strings(cfg.Paths)
	if err := config.Save(repo.Root, cfg); err != nil {
		return err
	}
	fmt.Printf("✓ eklendi: %s\n", strings.Join(added, ", "))
	fmt.Println("  (to publish: envmove)")
	return nil
}

// ---------------------------------------------------------------- doctor

func cmdDoctor(args []string) error {
	repo, err := gitx.Open()
	if err != nil {
		return err
	}
	fmt.Printf("repo     %s\n", repo.Root)

	if !config.Exists(repo.Root) {
		fmt.Println("config   ✗ missing — run `envmove setup`")
		return nil
	}
	cfg, err := config.Load(repo.Root)
	if err != nil {
		return err
	}
	fmt.Printf("config   ✓ %s (branch: %s, %d files)\n", config.FileName, cfg.Branch, len(cfg.Paths))

	if configIsDirty(repo.Root) {
		fmt.Println("config   ⚠ uncommitted — the other machines do not know this one yet")
		fmt.Printf("         git add %s && git commit -m \"...\" && git push\n", config.FileName)
	}

	url := repo.RemoteURL(syncer.Remote)
	if url == "" {
		fmt.Println("remote   ℹ none — state stays on this machine")
	} else {
		fmt.Printf("remote   ✓ %s\n", url)
		if strings.Contains(url, "github.com") && !cfg.PublicAcknowledged {
			fmt.Println("         ⚠ may be public, the risk has not been acknowledged")
		}
	}

	_, inKeychain, keyErr := loadKey(repo.Root)
	switch {
	case keyErr != nil:
		fmt.Printf("key      ✗ keychain: %v\n", keyErr)
	case inKeychain:
		fmt.Println("key      ✓ keychain'de")
	case keystore.RecoveryExists(repo.Root):
		fmt.Println("key      ✗ keychain'de yok — `envmove recover` ile geri getir")
	default:
		fmt.Println("key      ✗ no key for this machine — run `envmove setup`")
	}

	// The recovery copy is what makes a lost keychain survivable, so its absence is
	// reported as loudly as the key itself being missing.
	if !keystore.RecoveryExists(repo.Root) {
		fmt.Println("recovery  ✗ no recovery copy — losing the keychain means losing the data")
		fmt.Println("          run `envmove setup` again")
	} else {
		fmt.Printf("recovery  ✓ %s\n", keystore.RecoveryPath(repo.Root))
	}

	fmt.Printf("recipients %d\n", len(cfg.Recipients))
	if len(cfg.Recipients) == 0 {
		fmt.Println("          ⚠ no recipients, a snapshot cannot be encrypted")
	}

	s, err := buildSyncer(repo, cfg)
	if err != nil {
		return err
	}
	d, err := s.Status()
	if err != nil {
		return fmt.Errorf("could not read the state: %w", err)
	}
	if len(d.Conflicts) > 0 {
		fmt.Printf("\n⚠ %d conflicts\n", len(d.Conflicts))
		for _, p := range d.Conflicts {
			fmt.Println("  ", p)
		}
		return nil
	}
	fmt.Printf("\nstate    ✓ %d files in sync, %d waiting to be pushed\n",
		countKind(d, syncer.SameState), len(d.Outgoing))
	if len(d.Outgoing) > 0 {
		fmt.Println("         run `envmove`")
	}
	if suggestion := suggestExample(repo); suggestion != "" {
		fmt.Printf("\nexample   ⚠ %s\n", suggestion)
		fmt.Println("           run `envmove add .env.example` to opt it in, and the next")
		fmt.Println("           `envmove` run will generate it with empty values")
	}
	return nil
}

// ---------------------------------------------------------------- helpers

func buildSyncer(repo *gitx.Repo, cfg *config.Config) (*syncer.Syncer, error) {
	stored, ok, err := loadKey(repo.Root)
	if err != nil || !ok {
		if data, ferr := os.ReadFile(keystore.FallbackPath(repo.Root)); ferr == nil {
			stored, ok = data, true
		}
	}
	if !ok {
		if keystore.RecoveryExists(repo.Root) {
			return nil, fmt.Errorf("this machine's key is not in the keychain — restore it with `envmove recover`")
		}
		return nil, fmt.Errorf("no key for this machine — run `envmove setup`")
	}
	ids, err := age.ParseIdentities(strings.NewReader(string(stored)))
	if err != nil {
		return nil, fmt.Errorf("could not read the key: %w", err)
	}
	cipher, err := crypt.New(cfg.Recipients, ids)
	if err != nil {
		return nil, err
	}
	return syncer.New(repo, cfg, cipher), nil
}

func mergePaths(existing, added []string) []string {
	out := append([]string{}, existing...)
	for _, p := range added {
		if !contains(out, p) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func countKind(d *syncer.Diff, kind string) int {
	n := 0
	for _, c := range d.Changes {
		if c.Kind == kind {
			n++
		}
	}
	return n
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

var stdin = bufio.NewReader(os.Stdin)

func readLine() string {
	line, _ := stdin.ReadString('\n')
	return strings.TrimSpace(line)
}

// askYesNo asks a yes/no question.
//
// Both English and the shorter forms people actually type are accepted, so nobody has
// to think about the wording of a prompt to answer it.
func askYesNo(prompt string) bool {
	fmt.Printf("%s [y/n] ", prompt)
	switch normalise(readLine()) {
	case "", "y", "yes", "yeah", "sure", "ok", "e":
		return true
	}
	return false
}

func normalise(answer string) string {
	return strings.ToLower(strings.TrimSpace(answer))
}
