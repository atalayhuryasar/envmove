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
envmove watch    # optional: publish context even when you commit nothing
```

`recover`, `restore` and `rotate-key` are rarer still. Every one of them is a recovery
or inspection path, not part of daily work.

The rule that produced this shape:

> **A user who types no commands at all must be able to live in the whole scenario.**

That is what forces the design toward enforcement rather than reminders.

It is also a rule that git's own design breaks in one place. A commit that stages nothing
runs no hook, so a person who edits `.env` and commits nothing cannot be protected by any
hook. The rule survives by moving: `doctor` tells the truth about what is waiting, and
`watch` is there for anyone who wants the guarantee without the command. The claim was
wrong before the mechanism was; the mechanism arrived second.

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

### Not on every file change, but on every *finished* one

The obvious design is a filesystem watcher that pushes on change. It is wrong here: the
agent rewrites `.env` five times in a row, so each keystroke-batch would publish a
half-finished file, and a conflict could appear while nobody is looking.

The same watcher with a debounce is a different design, and it is `envmove watch`. A file
is published only after it has been completely quiet for a minute, so the half-finished
states exist on disk and never on the remote. Staging on write and publishing at session
boundaries stays the default; the watcher is opt-in, because a process that runs for days
on someone's machine is a thing they choose, not something a setup script installs behind
their back.

### The hole git cannot close

Staging on write and publishing at boundaries covers commits, pushes and agent sessions.
It does not cover editing a gitignored file and doing nothing else, because there is no
commit for a hook to fire on. No git hook fires for a working-tree change; git exits
before it gets that far.

That gap was found by a person doing exactly what the README told them to do. The fix is
`watch` plus an honest `doctor` line; the mistake was claiming a guarantee that git's
design does not allow.

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

### A baked path is still a path with a shelf life

Embedding the path was right and turned out to be half a solution. Homebrew deletes the
previous version's directory on upgrade, so a hook carrying
`/opt/homebrew/Cellar/envmove/0.3.1/bin/envmove` stopped working the moment 0.3.2 was
installed — and the symptom was not an error anyone would read, it was commits quietly
ceasing to carry context.

So a hook now carries a candidate list, tried in order:

1. the path it was installed with, **unless** that path is a Cellar directory
2. `/opt/homebrew/bin/envmove`, the symlink Homebrew keeps across upgrades
3. `$(command -v envmove)`

The Cellar exclusion is the whole trick. A version number in a path is a promise about a
directory that will be deleted; the opt symlink is a promise about the install. And a
build from source keeps its own path first, because someone testing a fix needs the hooks
to run that fix rather than an older brew install they have forgotten about.

When every candidate fails, the hook warns on stderr and exits 0. A missing envmove must
not make the repository uncommittable: a hook that blocks every commit gets deleted by
the person it is blocking, and then the sync is lost permanently rather than temporarily.

`envmove doctor` reports which binary the hook resolves to and compares it against the
one being run. That comparison found the problem while it was still being worked on.

### A commit that changed nothing runs no hook at all

The promise was "you never type envmove". Editing `.env`, then running `git commit`,
broke it: `.env` is gitignored, git stages nothing, git bails out before any hook, and
the state sat there. `post-commit` cannot help — there is no commit.

This was found by running the journey, not by reading the code. The hooks are correct and
the promise was still wrong.

`envmove watch` closes it, opt-in, publishing once files have been still for a minute.
The debounce is the design: the version rejected earlier — publish on every write — would
ship a half-written `.env` every time an agent rewrote one, which is the failure mode the
baseline cannot resolve because neither side wrote a complete file.

`envmove doctor` reports the pending count either way, so the state is never invisible.

### The fast path cannot live inside `Push`

A commit that touched only code took fourteen seconds, on a connection where `git fetch`
alone took four.

The fix reads naturally and is wrong in two places:

- comparing the working tree to the local baseline is free, so the check goes at the top
  of `Push`, before any network call. Placed after the fetch it saves nothing — the round
  trip has already been paid.
- `sync` calls `Pull` **first**, and `Pull` fetches. So even a correct fast path inside
  `Push` runs after the cost is already incurred. Measured: 47 ms against 11 s, once the
  check was lifted to `Publish`, which is what the hooks actually call.

A hook calls the operation, not the step. The optimisation belongs where the hook is.

### Hooks publish; humans and sessions sync

`Publish` skips the network when nothing local changed. `Sync` pulls and then pushes,
because a session starting needs to catch up on the other machine and not just leave
something behind. Mixing them costs either correctness or latency, so they are separate
methods with separate callers.

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

**v0.3** — the detection model inverted: carry everything git ignores, subtract a junk
list. `.envmoveignore` is a deny-list; `secretPatterns` and `agentPatterns` only label. **Done.**

**v0.4** — `watch`, hooks that survive a Homebrew upgrade, a fast path so a code-only
commit costs no network, `doctor` reporting the hooks it finds. **Done.**

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
  generator, detection, the briefing, the hook scripts and the idle publisher
- six end-to-end scenarios that build two working copies against a bare remote and
  walk the whole loop, with the key store exercised for real — including a real
  `git commit`, because the hooks are the mechanism and nothing else tested them

What has **not** been verified: a real project on two physically separate machines was
the long-standing gap, and it is now done. A private repository has two Macs registered,
two keychains, and a `.env.local` that travelled between them and back.

That journey is worth more than any feature on the list above, because every one of these
came out of it and none came out of a test:

| found | why no test found it |
|---|---|
| a commit with nothing staged runs no hook | the promise was about git behaviour, not envmove |
| hook pinned to a Cellar path breaks on upgrade | needs an upgrade and a following commit |
| 14 s per code-only commit | needs a slow network and a stopwatch |
| `doctor` blind to a stale hook | needs an install that has aged |
| nil-pointer panic when a pull fails at session start | needs a pull that fails |

The lesson is not "write more tests", though there are more tests now. It is that a tool
whose whole promise is "it just works" has to be walked end to end by a person before its
claims are true, and the walk has to happen on the hardware the claims are about.

Still unverified: two people editing the same file on two machines at the same time, and a
repository whose `.gitignore` covers a very large number of files.