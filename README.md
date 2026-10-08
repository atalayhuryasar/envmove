# envmove

**The layer git won't carry.**

You pushed your code. Your agent still forgot everything.

```bash
$ envmove
↓ .env  ↓ HANDOVER.md
```

---

## The problem

Git carries your code. It does not carry:

| | what | why it's excluded |
|---|---|---|
| `.env` | secrets | never commit |
| `HANDOVER.md` | where the work stopped | commit noise |
| `TODO.md` | what comes next | commit noise |
| `.claude/settings.json` | how your agent behaves | varies per machine |

Two different reasons, same result: **this state does not travel between your
machines.** You develop on the laptop, switch to the desktop, and the project is
half-restored. Then your AI agent opens a repository it has never seen and starts
re-solving problems you settled on Tuesday.

Syncing those files by hand works. It also stops working the first week, because it
depends on you remembering.

## What envmove does

It carries the state git refuses to, encrypted, over the git remote you already have.

```
main        your code
envmove     one encrypted file: state.age
```

- **No server.** No account. No new credential. It rides on the remote you configured.
- **No server-side secret.** Encryption happens on your machine; the private key never
  leaves it.
- **Files stay plaintext on disk.** `vim .env`, `docker compose up` — everything
  works exactly as before. Encryption exists only in transit.
- **No commands.** After setup you type nothing. Git and agent hooks do the work.

## Install

```bash
go install github.com/atalayhuryasar/envmove/cmd/envmove@latest
```

Or with Homebrew, from the formula in this repository:

```bash
brew tap atalayhuryasar/tap
brew install atalayhuryasar/tap/envmove
```

envmove is a single static binary with no runtime dependencies beyond `git` and macOS.

## Setup, once per machine

```bash
cd ~/dev/myproject
envmove setup
```

It finds the files worth carrying, shows them with a reason each, asks once for a
recovery passphrase, and wires up git and agent hooks.

```
tespit edilen dosyalar:
  .env                       secret — .env
  HANDOVER.md                agent state — HANDOVER*
  TODO.md                    agent state — TODO*

[hepsi/h]  hepsini dahil et
```

Setup offers to commit and push `envmove.toml` for you, which is how the second machine
learns you exist. That is the whole setup; you will not be asked for anything again.

## Day to day

Nothing. That is the point.

```
you edit .env       → nothing happens yet
you git commit      → context is pushed alongside the code
you git push        → refused if context was left behind
agent session starts→ context is pulled, briefing is printed
agent session ends  → context is pushed
```

You only type `envmove` when something is wrong. Then:

```bash
envmove doctor
```

## What your agent sees

At the start of a session, before it reads anything:

```markdown
# myproject — oturum brifingi

## Where things were left
Stripe retry logic is half done. test_retry_backoff is failing.

## Open tasks
- [ ] add retry backoff
- [ ] webhook idempotency

## Not yet synced
- .env

_1 file waiting to be pushed._
```

An agent that knows where you stopped does not start from zero.

## Commands

You rarely type any of them. These are for the other ninety percent of the time.

```bash
envmove            # sync (hooks do this for you)
envmove setup      # install on this machine
envmove add X      # carry another file
envmove doctor     # explain what is wrong
envmove recover    # restore the key if the keychain is gone
envmove restore    # put the project back the way it was
envmove rotate-key [--all]
```

### Getting back to a working state

You broke something on Friday and need Tuesday back:

```bash
envmove restore        # lists snapshots, newest first
envmove restore 2d     # or name a span
envmove restore abc1234 --force
```

It refuses to run if it would discard work that was never pushed. Work that is already
on the branch does not count: that is what the list above it is for.

### Changing the lock

```bash
envmove rotate-key        # new key for this machine
envmove rotate-key --all  # drop every recipient, including other machines
```

Rotating protects what comes next. Snapshots already in git history stay encrypted to the
keys they were written with.

### Keeping .env.example honest

envmove reads `.env` and updates the example file next to it: new variables appear,
deleted ones disappear, and the comments you wrote stay.

It copies neither values nor comments out of `.env`. A comment reading `# the prod master
password` does not belong in a published file.

```bash
envmove add .env.example   # opt in, once
```

## Encryption

| | where | when |
|---|---|---|
| public key | `envmove.toml`, committed | safe to publish |
| private key | macOS Keychain | never leaves the machine |
| recovery copy | `~/.config/envmove/recovery/`, passphrase-wrapped | disaster only |

No passphrase is ever asked during normal use. During setup you are shown **one**
recovery passphrase; it is the only way back if the keychain is lost, so put it in
1Password.

Losing both the keychain and that passphrase means losing the data. This is the same
rule every encrypted system follows.

## Public repositories

Encrypted state on a public remote is **permanent**. Anyone can read the ciphertext. If
the key is ever compromised, so is every snapshot, past ones included.

| what | where |
|---|---|
| local dev secrets, personal API keys | envmove |
| `.env` schema, example values | `main` → `.env.example` |
| **production secrets** | **1Password / Doppler** |

For open source, envmove's most useful job is not carrying secrets — it is keeping
`.env.example` and the working setup reproducible for contributors.

## Not this

envmove is for state that belongs to *you* and travels between *your* machines. Not:

| | use instead |
|---|---|
| team secrets | 1Password, Doppler, Infisical |
| CI/CD secrets | SOPS |
| a reproducible environment | Nix, devcontainer |
| Linux or Windows | write your own, or do not |

## How it works

- One encrypted snapshot per sync, stored as `state.age` on the `envmove` branch. Atomic:
  a half-synced workspace is not representable.
- `age` for encryption. `git merge-file` for three-way comparison.
- A local baseline records the last synced state, which is what lets envmove tell an
  edit apart from a disagreement.
- Absolute paths inside files are rewritten to placeholders on the way out and restored
  on the way in, so a snapshot taken on a laptop opens correctly on a desktop.

`DESIGN.md` records the reasoning, including the mistakes worth not repeating.

## Status

**envmove is macOS only.** The private key lives in the login keychain and the tool does
not pretend otherwise on other platforms.

That is a deliberate choice, not an unfinished one. Only one piece of envmove is
platform specific, the place the key is kept between runs. Everything else — state,
merging, detection, the agent briefing — is ordinary Go, and `go test ./...` runs
anywhere, so a Linux contributor can work on most of this without a Mac.

What macOS only costs: two working copies, one remote, is the test setup, and it needs a
real keychain, so the end-to-end scripts are macOS only too.

```bash
go test ./...        # anywhere
bash test/e2e.sh     # macOS: two checkouts, a bare remote, the whole loop
```

There is no daemon, no server and no account.

## Contributions

`DESIGN.md` is the source of truth, including the mistakes worth not repeating: why
merging ciphertext does not work, why envmove must never block a code push, why a
passphrase should not be case-folded, and why `restore` only protects work that was
never pushed.

## License

MIT