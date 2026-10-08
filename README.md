<div align="center">

# envmove

**Your `.env` and your agent's memory, carried between machines.**

Encrypted. Over the git remote you already have. Zero commands to run.

[![CI](https://github.com/atalayhuryasar/envmove/actions/workflows/ci.yml/badge.svg)](https://github.com/atalayhuryasar/envmove/actions/workflows/ci.yml)
[![macOS](https://img.shields.io/badge/macOS%20only-9ca3af?style=flat-square)](https://github.com/atalayhuryasar/envmove)
[![Go](https://img.shields.io/badge/go-1.23%2B-00ADD8?style=flat-square)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

</div>

---

## The gap

Git carries your code. It refuses to carry everything else:

| file | what it holds | why git won't take it |
|---|---|---|
| `.env` | your secrets | never commit |
| `HANDOVER.md` | where you stopped | commit noise |
| `TODO.md` | what's next | commit noise |
| `.claude/settings.json` | how your agent behaves | differs per machine |

Two different reasons, one result: **this state does not travel.** You work on the
laptop, move to the desktop, and the project is half-restored.

Then your AI agent opens a repository it has never seen and starts from zero —
re-solving problems you settled on Tuesday.

Copying those files by hand works. It also stops working in the first week, because it
depends on you remembering.

## What envmove does

It carries that state between machines, encrypted, over the git remote you already
have. Nothing else changes:

```
main       your code
envmove    one encrypted file: state.age
```

- **No server, no account, no new credential.** It rides on the remote you configured.
- **Files stay plaintext on disk.** `vim .env`, `docker compose up`, your tools: all
  unchanged. Encryption happens in transit only.
- **Nothing to run.** Git and agent hooks do the work after a one-time setup.
- **Paths are rewritten per machine.** A `REPO_ROOT` written on the laptop opens
  correctly on the desktop.

## Install

```bash
brew tap atalayhuryasar/tap
brew install atalayhuryasar/tap/envmove
```

```bash
# or, without Homebrew
go install github.com/atalayhuryasar/envmove/cmd/envmove@latest
```

One static binary. The only requirements are `git` and macOS.

> **macOS only.** The private key lives in the login keychain. This is a deliberate
> choice, not an unfinished port — see [Status](#status).

## Quickstart

Once per machine, in a repository:

```bash
cd ~/dev/myproject
envmove setup
```

```
files detected:
  .claude/settings.json                    agent state — .claude/**
  .env                                     secret — .env
  HANDOVER.md                              agent state — HANDOVER*
  TODO.md                                  agent state — TODO*

[all]  include all   [one]  choose one by one   [none]  skip everything
+ this machine's public key added: age1t9ezarr5…

✓ envmove.toml written (4 files)
✓ envmove.toml published
✓ git hooks installed (post-commit, pre-push)
✓ claude wired up → .claude/settings.local.json
· .env.example updated (+2: SECRET, REPO_ROOT)

Done. 4 files will travel encrypted on the "envmove" branch.
Nothing else to do — git and agent hooks take it from here.
```

That is the whole setup. On the second machine, run the same command.

## Day to day

You type nothing.

```
you edit .env         → staged, not published yet
you git commit        → context is pushed alongside the code
you git push          → refused if context was left behind
agent session starts  → context pulled, briefing printed
agent session ends    → context pushed
```

Two things happen on their own that are worth seeing once:

**Your `.env.example` stays current.** envmove reads `.env` and updates the example
file next to it — new variables appear, deleted ones disappear, your comments stay:

```diff
   # database settings
   DB_HOST=localhost
   DB_PORT=5432
+
+  SECRET=
+
+  REPO_ROOT=
```

It copies neither values nor comments out of `.env`. A comment reading
`# the prod master password` does not belong in a published file.

**Your agent gets a briefing** before it reads anything:

```markdown
# myproject — session briefing

## Where things were left
Stripe retry logic is half done. test_retry_backoff is failing.

## Open tasks
- [ ] add retry backoff
- [ ] webhook idempotency

_Everything is in sync._
```

An agent that knows where you stopped does not start from zero.

## Commands

Four, and two of them rare.

| command | when |
|---|---|
| `envmove` | almost never — hooks do this for you |
| `envmove setup` | once per machine |
| `envmove add <path>` | a file needs carrying |
| `envmove doctor` | something looks wrong |
| `envmove recover` | the keychain is gone |
| `envmove restore` | you broke something |
| `envmove rotate-key` | changing the lock |

<details>
<summary><b>Getting back to a working state</b></summary>

```bash
envmove restore          # list snapshots, newest first
envmove restore 2d       # or name a span
envmove restore abc1234 --force
```

```
snapshots, newest first:
  1) 2026-10-08T21:14  e7b556c  envmove: 0 files updated   ← current state
  2) 2026-10-08T19:02  6e2b55d  envmove: 4 files updated

  to go back, pick 2 or later
```

Only work that was **never pushed** is protected. Work already on the branch does not
count — that is what the list above it is for.

</details>

<details>
<summary><b>Changing the lock</b></summary>

```bash
envmove rotate-key         # new key for this machine
envmove rotate-key --all   # drop every recipient, other machines included
```

Rotating protects the future. Snapshots already in git history stay encrypted with the
keys they were written with.

</details>

## Security

| | where | when |
|---|---|---|
| public key | `envmove.toml`, committed | safe to publish |
| private key | macOS Keychain | never leaves the machine |
| recovery copy | `~/.config/envmove/recovery/`, passphrase-wrapped | disaster only |

Setup shows you **one** recovery passphrase. Day to day it is never asked; the
keychain serves. It exists for the day the keychain does not. Put it in 1Password.

Losing both the keychain and that passphrase means losing the data. That is the rule
every encrypted system follows.

**On a public remote, encrypted state is permanent.** Anyone can read the ciphertext. If
the key is ever compromised, every snapshot is exposed, past ones included.

| what | where |
|---|---|
| local dev secrets, personal API keys | envmove |
| `.env` schema, example values | `main` → `.env.example` |
| **production secrets** | **1Password / Doppler** |

envmove encrypts everything, always. It has no "public mode" with weaker rules, because
two rule sets means two ways to get this wrong.

## Not this

envmove carries state that belongs to *you* between *your* machines. For anything else:

| need | use instead |
|---|---|
| secrets shared by a team | [1Password](https://1password.com), [Doppler](https://www.doppler.com), [Infisical](https://infisical.com) |
| CI/CD secrets | [SOPS](https://github.com/getsops/sops) |
| a reproducible environment | [Nix](https://nixos.org), [devcontainer](https://containers.dev) |
| Linux or Windows | envmove will refuse to run |

## How it works

- One encrypted snapshot per sync, stored as `state.age` on the `envmove` branch. Atomic:
  a half-synced workspace is not representable.
- [age](https://github.com/FiloSottile/age) for encryption; the private key never leaves
  the machine, so the server only ever holds ciphertext.
- A local baseline records the last synced state. That is what lets a three-way
  comparison tell "I edited this" apart from "we both edited this".
- Absolute paths inside files become placeholders on the way out and are restored on the
  way in.

[DESIGN.md](DESIGN.md) records the reasoning, including the mistakes worth not repeating.

## Status

macOS only, and verified there. v0.2.0.

```
7 unit test packages · 5 end-to-end scenarios · 2 CI jobs (Linux + macOS)
```

The end-to-end scenarios build two working copies against a bare remote and walk the
whole loop with a real keychain. What they do **not** cover, and what is worth knowing
before you rely on it: two physically separate machines, and first-run behaviour on a
repository that already has a Claude Code setup.

## Contributing

```bash
make test    # unit tests, anywhere
make lint
make e2e     # macOS: all five scenarios, plus the global-config guard
```

The Linux CI job runs the portable suite, so you do not need a Mac to work on most of
this.

`DESIGN.md` is the source of truth for why things are the way they are. If you change
behaviour, change the reasoning too.

## License

[MIT](LICENSE)