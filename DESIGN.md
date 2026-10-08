# envmove — design notes

> This document is the source of truth for the implementation. The README (English)
> is written afterwards. Everything here exists to explain reasoning, including the
> mistakes worth not repeating.

---

## The problem

Git carries your code. It refuses to carry the rest:

| layer | what | example | why it is excluded |
|---|---|---|---|
| **T1** | committable project definition | `schema.prisma`, `Dockerfile` | nothing, it is committed |
| **T2** | **git ignores it, envmove carries it** | `.env`, `.env.local`, `HANDOVER.md`, `.superpowers/` | secrets or noise |
| **T3** | git ignores it, envmove subtracts it | `node_modules`, `.next`, `*.log` | regenerable |

The gap is T2. Files end up there for two different reasons: **privacy** (`.env` must
never be committed) and **noise** (nobody wants `HANDOVER.md` in their history). One
result, two intents, and no tool covers both.

The real damage is not the inconvenience. AI agents read these files and **make
decisions** from them. Context that does not travel means an agent opens a repository
it has never seen and starts with amnesia, re-solving problems that were settled days
ago.

> **envmove — the layer git will not carry.**
> *"You pushed your code. You did not push your conversation."*

---

## The rule: carry everything git ignores, subtract the junk

Git already knows which files belong to this developer and not to the project. That
set is exactly what it ignores. envmove starts from the whole set and subtracts.

**The other direction was tried and does not scale.** Matching a list of filenames
meant every new AI tool needed another entry. The exclusions went from `.claude` to
`.cursor` to `.opencode` to `.superpowers`, which appeared in three separate real
repositories, and the next tool would need another pattern. Subtracting junk does not
have that problem, because junk is a short, stable, well known list.

Measured against three real repositories:

| repository | gitignored | carried | skipped |
|---|---|---|---|
| Rose Sole (Next.js + Prisma) | 41054 | 1 | 41053 |
| Supapulse (Next.js) | 35086 | 3 | 35083 |
| mailto (Swift) | 4657 | 14 | 4643 |

The permission was real: the first version without a `generated` exclusion carried
twenty-four files of Prisma client out of a single repository. A permissive default
needs the exclusion list to be tuned against real checkouts, and it is.

### Two boundaries worth stating precisely

**Gitignored, not untracked.** A file that is untracked but not ignored is on its way
into a commit. Carrying it would collide with the commit the developer is about to
make, and would then travel to the other machine as a surprise duplicate.

**Nothing happens invisibly.** Setup lists every file it will carry with a reason, and
summarises skips by cause rather than one by one, because a repository with forty
thousand ignored files would otherwise bury the two that matter.

### `.envmoveignore` is a deny list and only a deny list

There is deliberately no way to match a file *in*. That is git's job: if a file should
be in the repository, the answer is to un-ignore it, not to teach a second tool about
it. One direction means the file cannot be got wrong, and `envmove add` still exists
for the case where a file is ignored by a pattern that also covers files you want.

### Why the exclusion list is a list of directories, not files

`node_modules/` in a real project is forty thousand files. Listing them to decide not
to carry them is wasted work, and the decision is one line rather than forty thousand.
Exclusions are matched as whole path segments, so a project called `build-tools` is
never mistaken for a `build` directory.

## Positioning

| tool | what it solves | what it does not |
|---|---|---|
| git | T1 | T2 |
| 1Password / Doppler / Infisical | the secret subset of T2 | project context, paths, agent state |
| chezmoi / yadm / stow | the home directory | the project directory, one project across machines |
| Nix / devcontainer | the environment | the conversation that was in progress |
| **envmove** | **all of T2, plus paths, plus agent continuity** | T3 |

### The alternative it has to beat

Syncing those files by hand works today:

```bash
# keep state in a private repo, symlink it into place
ln -s ~/state/myproject/env ./myproject/.env
```

This is a real option and envmove has to be meaningfully better or nobody will use it:
the symlink has no merge semantics, no conflict detection, no agent integration and no
path rewriting. It is named here as the benchmark, not hidden.

---

## Decisions taken

| question | answer | consequence |
|---|---|---|
| two machines | **macOS and macOS** | path templating shrinks to two substitutions; Keychain is native |
| primary value | **agent continuity** | agent state detection is first class, not an afterthought |
| secrets on a public remote | **encrypted anyway, with a loud warning** | one code path instead of two; loss is contained by the warning |

---

## Architecture

### Transport: no new server

The git remote **already is** a sync channel. A new account, host or credential means
adoption starts at zero, so there is none.

### Storage: one branch, one file, one blob

```
github.com/you/project
  ├── main       code
  └── envmove    one encrypted file: state.age
```

A single snapshot per sync is deliberately chosen over per-file storage:

- **atomic.** A half-synced workspace is not representable. With per-file blobs it is,
  and a torn state is worse than a stale one.
- no manifest, no merge-base bookkeeping, half the code
- **the cost:** two machines editing the same file at the same time is reported rather
  than merged. That trade is correct while usage is sequential, which is what the
  three-way merge below depends on.

### Merge: three-way, against a local baseline

Ciphertext cannot be merged (SOPS works around this by making the user run
`git-crypt unlock` first). So the comparison happens on plaintext and the hard part is
handed to git: `git merge-file` does a real three-way merge, and this project does not
reimplement it.

Distinguishing "I changed this" from "we both changed this" needs a third reference.
Without one, every local edit looks like a conflict. So the last synced snapshot is
kept locally in `.envmove/baseline.age`:

| local | baseline | remote | verdict |
|---|---|---|---|
| changed | changed | same | ours to publish |
| same | changed | changed | take theirs |
| changed | changed | changed | conflict, touch nothing |
| absent | absent | present | incoming; nothing local to lose |

The baseline is encrypted too. It never leaves the machine and is not in git, but it
holds the same secrets, so plaintext is not an option.

### Encryption

- `age` (X25519). One keypair per machine.
- Public key in `envmove.toml`, committed and safe to publish.
- Private key in the **macOS keychain**, never in the repository.
- No passphrase is ever asked during normal use. The keychain serves day to day; the
  recovery copy exists for the day it does not.

---

## The command surface: four, and two of them rare

```bash
envmove setup    # once per machine
envmove          # sync (rarely typed: hooks do this)
envmove add X    # a new file needs carrying
envmove doctor   # when something is wrong
```

`recover`, `restore` and `rotate-key` are rarer still. Every one of them is a recovery
or inspection path, not part of daily work.

The rule that produced this shape:

> **A user who types no commands at all must be able to live in the whole scenario.**

That is what forces the design toward enforcement rather than reminders.

---

## Automation: four layers

| # | layer | fires on | needs the agent? |
|---|---|---|---|
| 1 | git hooks (`post-commit`, `pre-push`) | commit, push | no |
| 2 | agent hooks (`SessionStart`, `Stop`) | session start, session end | no |
| 3 | instruction files (Cursor, AGENTS.md) | model judgement | yes |
| 4 | MCP server | model call | yes |

Layers 1 and 2 are **deterministic**: they work in a terminal with no agent running.
Layers 3 and 4 only make an agent smarter. Presenting them as equally reliable would be
the wrong kind of optimistic.

```
SessionStart  → fetch, decrypt, merge, write, then print a briefing
post-commit   → encrypt and push
pre-push      → push any pending context first; refuse if a conflict is unresolved
Stop          → final push
```

The briefing is the part that turns a sync tool into an agent tool. An agent that knows
where the work stopped does not start from zero.

### No auto-push on every file change

The obvious design is a filesystem watcher that pushes on change. It is wrong here: the
agent rewrites `.env` five times in a row, so each keystroke-batch would publish a
half-finished file, and a conflict could appear while nobody is looking. Staging on
write and publishing at session boundaries avoids all of it.

---

## Agent integration: two mechanisms, on purpose

| tool | mechanism | reliability |
|---|---|---|
| Claude Code | `SessionStart` and `Stop` hooks in `.claude/settings.local.json` | deterministic |
| Cursor | `.cursor/rules/envmove.mdc` with `alwaysApply` | model-dependent |
| Codex / generic | a section in `AGENTS.md` | model-dependent |

Hooks go in **`settings.local.json`**, never `settings.json`. That file is per-machine
by design and is never committed. Writing hooks into the shared file would carry one
machine's absolute binary path to another machine, where it does not resolve. Detection
excludes it explicitly.

A hook never fails a session. A sync problem is reported and the agent continues,
because an agent that refuses to start is worse than one that starts with slightly
stale context.

---

## Security model

1. **Everything is encrypted with `age`.** There is no public and private code path,
   which means there is only one set of rules and one set of bugs.
2. **A public remote is detected** and acknowledged once, explicitly. An encrypted
   snapshot on a public remote is permanent: anyone can read the ciphertext, and if the
   key is ever compromised, every snapshot ever written is exposed.
3. **Detection is fail-closed.** Anything matching a secret pattern is encrypted unless
   the user explicitly says otherwise.
4. **Production secrets do not belong here.** For an open source repository the useful
   job is keeping `.env.example` and the setup reproducible, not publishing production
   credentials. Those go in 1Password or Doppler.
5. **No automatic history rewriting.** If a secret lands in history, envmove warns and
   points at `git filter-repo`. Silently rewriting someone's history is not a decision a
   sync tool should make on its own.

### The keychain is a single point of failure, so there is a recovery copy

A reinstalled macOS, a wiped laptop or a forgotten login password would otherwise mean
losing every secret the repository ever carried.

`setup` writes a passphrase-wrapped copy of the private key
(`~/.config/envmove/recovery/<repo>.agekey`, scrypt, 0600) **before doing anything
else**, and `envmove recover` puts it back.

Two details that are easy to get wrong:

- **The passphrase is trimmed of whitespace and nothing else.** An earlier version also
  folded case and stripped dashes. That is friendlier to type and a real weakness:
  folding makes `secret` and `SECRET` the same key, quietly shrinking the search space.
  Being forgiving about a passphrase is the wrong place to trade strength for
  convenience.
- **It is asked in a loop and setup fails if it cannot be written.** With a single
  attempt, one typo used to end setup with the key in the keychain and no recovery copy
  at all, while still reporting success.

---

## Decisions that came out of writing the code

### Hooks carry an absolute path to the binary

`envmove sync` cannot rely on `PATH`. GitHub Desktop and some IDEs launch git with a
minimal environment; when `envmove` is missing the hook fails, and a failing pre-push
hook silently blocks every push. Hooks therefore embed `os.Executable()`.

### The recursion had to be closed in two places

envmove pushes its own branch with `git push`, which fires the repository's own
pre-push hook, which runs envmove, which pushes again.

- the hook sets `ENVMOVE_NO_PUSH=1`, so its own sync does not publish
- envmove's push is marked `ENVMOVE_INTERNAL=1`, and `main` exits when it sees it

### `git commit --only` needs a path

Committing the config without sweeping in whatever else the user had staged requires
naming the file: `git commit --only -m ... -- envmove.toml`. A sync tool that quietly
absorbed unrelated staged work into its own commit would be a menace.

### envmove never blocks a code push

Only an unresolved conflict blocks, because that is the one case where pushing would
silently destroy work. Everything else warns and lets the push through.

This closed two deadlocks, both with the same root cause:

1. A new machine could not push its own `envmove.toml`, because the push hook treated
   the snapshot it could not yet decrypt as a failure and refused.
2. Publishing a config change was blocked by a conflict, so a conflict could not be
   resolved by publishing the config change that resolves it.

Hence: hook mode warns and exits 0, and config publishing uses `--no-verify`.

### A new machine triggers re-encryption

The snapshot records which recipients it was encrypted to. When the config gains a
machine, the next push re-encrypts **even if no file changed**, which is what lets a
machine that joined yesterday read the history from before it arrived.

It is a one-time feedback step, stated plainly in the setup output, and `doctor` flags
an uncommitted config.

### Deletions travel too

`pull` applies removals as well as additions. Otherwise a deletion only ever travelled
one way and the two machines slowly drifted to different file sets.

### Path matching and macOS symlinks

`git rev-parse --show-toplevel` returns a fully resolved path (`/private/tmp/x`) while
the value inside a file may be spelled unresolved (`/tmp/x`). On macOS `/tmp` and `/var`
are symlinks, so this is a real source of silent failure.

When the direct match fails, each path-like token in each line is retried after
resolving its own directory. Replacement is boundary-aware, so `~/dev/app-backup` is
never mistaken for a child of `~/dev/app`.

### Matching is per line, not per file

`Contract` was first written to match a whole path. Applied to file contents it never
matched anything: a `.env` line reads `REPO_ROOT=/Users/me/dev/app`, so the path
appears partway through a line. Caught by a test, not by reading the code.

### The test suite must not touch global git config

The scripts once ran `git config --global user.name`. Running the suite replaced the
machine owner's name and email, and every commit they made afterwards was attributed to
`envmove test`. It surfaced in a real project.

Identity is now passed only through `GIT_AUTHOR_*` and `GIT_COMMITTER_*`. The side
effect is that the tests no longer depend on the user's global config, so they also run
in CI.

`test/guard.sh` compares global config before and after and fails with the diff. A
`--global` write creeping back in does not pass quietly.

> The only config a test script should ever write is its own throwaway repository.

---

## `.env.example` generation

For an open source project the most useful job is not carrying secrets, it is keeping
`.env.example` honest. A newcomer copies it, fills in two variables and runs. When it
drifts, they lose an hour to a connection error instead of building something.

Two rules, both for safety:

- **Real values are never copied.** A value comes from the existing example or stays
  empty.
- **Comments are never copied out of `.env` either.** A comment reading
  `# the prod master password` does not belong in a published file.

Only a file git **already tracks** is rewritten. Adding a `.env.example` to someone's
repository uninvited is not helpful. `doctor` reports a missing one; `envmove add
.env.example` opts in.

---

## `restore` and `rotate-key`

`restore` is not a sync feature, it is a way back from having broken something.

The refusal rule is narrow on purpose: only work that was **never pushed** is
protected. The usual reason to restore is that the current state is wrong, and that
state is sitting in the snapshot list one entry up. Requiring `--force` for that would
make the safety net unusable exactly when it is needed.

The newest entry is marked `← current state`. "Go back" must not mean "reload the
broken state you were trying to escape".

`rotate-key` replaces the key, re-encrypts the current snapshot **and rewrites the
recovery copy around the new key**. Leaving the old recovery file in place would look
like working disaster recovery while restoring a key that can no longer read anything,
so the passphrase is asked for and rotation is refused without it.

Snapshots already in history stay encrypted with the keys they were written with.
Rotating protects the future, not the past, and the output says so.

---

## Platform: macOS only, deliberately

envmove runs on macOS. That is a decision, not an unfinished job, and this document
exists so it stays one.

Exactly one place is platform specific: where the key is kept between runs
(`internal/keystore`). Everything else — state, merging, detection, the agent briefing,
the example generator — is ordinary Go and is tested anywhere.

The reasoning was a choice between:

- putting the key in 1Password: less code, more moving parts, worse failure modes. One
  `security` invocation is enough.
- generating shell profiles: a weak feature on macOS that does not port.

The cost is stated plainly: the test setup needs two checkouts and a real login
keychain, so the end-to-end scripts are macOS only. The mitigation is a Linux CI job
for the portable suite, so a contributor who has never touched a Mac can still run the
tests and open a pull request. macOS only stays a product decision rather than a wall.

### Why `keystore` is its own package

The interface (`Store`) and the macOS implementation are separate, so a second
implementation is one package away and nothing else has to know.

The side benefit is a test store that is **deliberately not persistent**. A store that
quietly wrote secrets to disk would be a hole in the one package whose entire job is to
avoid that.

The recovery copy is not in the keychain, it is plain filesystem, so it works the same
wherever envmove runs.

---

## Roadmap

**v0** — setup, sync, add, doctor, recover, age, single snapshot, git hooks, automatic
detection, path rewriting. **Done.**

**v0.1** — recovery passphrase, agent hooks and the session briefing. **Done.**

**v0.2** — `.env.example` generator, `restore`, `rotate-key`, automatic config
publishing, Homebrew formula. **Done.**

**Next** — a warning when several people share one repository and should each use their
own branch. That is a security hole rather than a feature: two machines adding their
keys to one branch means every snapshot is encrypted to both, and each can read the
other's `.env`.

`detect` now starts from the whole gitignored set and subtracts, with `.envmoveignore`
as the single override. `secretPatterns` and `agentPatterns` survive, but only as
labels in the setup output: they no longer decide whether a file travels.

**Not planned** — Linux and Windows support.

---

## State of the work

Everything is verified by tests, and the tests are honest about their limits:

- unit tests for the merge policy, path rewriting, key rotation, the example
  generator, detection and the briefing
- five end-to-end scenarios that build two working copies against a bare remote and
  walk the whole loop, with the key store exercised for real

What has **not** been verified: a real project, a real `.env`, a real agent session,
and two physically separate machines. The first-run experience on a repository with an
existing Claude Code setup is unproven. That is the next thing to do, and it is worth
more than any feature on the list above.