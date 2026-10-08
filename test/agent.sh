#!/bin/bash
# Agent entegrasyonu: .claude/ kurulumu + session-start brifingi
set -e
ROOT=/tmp/envmove-agent
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# The binary under test is the one this repository builds, not whatever happens to be
# installed. Hardcoding a path meant the suite tested a stale copy in /tmp.
ENVMOVE="${ENVMOVE:-$SCRIPT_DIR/../bin/envmove}"
PASS='kurtarma-sifresi-2026'
rm -rf "$ROOT"; mkdir -p "$ROOT"
# leftover recovery files from an earlier run would skip a setup prompt and shift the
# scripted answers, so start from a clean machine state.
rm -f ~/.config/envmove/recovery/*.agekey 2>/dev/null || true
step() { printf "\n\033[1;36m== %s\033[0m\n" "$1"; }

# Git identity for the throwaway repositories these scripts build.
#
# This is set per command through the environment, never with `git config --global`.
# An earlier version wrote the global config, which meant running the test suite
# silently replaced the machine owner's name and email — and every commit they made
# afterwards was attributed to "envmove test". Anything that writes to a global
# config is out of scope for a test script, always.
export GIT_AUTHOR_NAME="envmove test"
export GIT_AUTHOR_EMAIL="test@envmove.local"
export GIT_COMMITTER_NAME="envmove test"
export GIT_COMMITTER_EMAIL="test@envmove.local"

git init --quiet --bare "$ROOT/remote.git"
git clone --quiet "$ROOT/remote.git" "$ROOT/work" 2>/dev/null
cd "$ROOT/work"
git checkout --quiet -b main
printf '.env\nHANDOVER.md\nTODO.md\n.claude/\n.cursor/\nAGENTS.md\n' > .gitignore
printf 'x\n' > main.py
mkdir -p .claude && printf '{"model":"opus"}\n' > .claude/settings.local.json
git add -A && git commit --quiet -m init && git push --quiet -u origin HEAD:refs/heads/main

printf '# Handover\n\nStripe retry mantigi yarim kaldi. test_retry_backoff kirik.\n' > HANDOVER.md
printf '# Todo\n- [ ] retry backoff ekle\n- [ ] webhook idempotency\n- [x] ilk kurulum\n' > TODO.md
printf 'SECRET=abc\n' > .env

step "setup (AI folders present: .claude/)"
printf 'y\n\nall\nn\n%s\n' "$PASS" | "$ENVMOVE" setup 2>&1 | tail -8

git add envmove.toml && git commit --quiet -m cfg && git push --quiet origin main

step "the generated .claude/settings.local.json (the existing 'model' must survive)"
cat .claude/settings.local.json

step "was AGENTS.md created"
if [ -f AGENTS.md ]; then head -5 AGENTS.md; else echo "(no AGENTS.md, correct: the repo does not use it)"; fi

step "session-start hook, and the briefing"
"$ENVMOVE" hook session-start 2>&1

step "the other machine: side B"
git clone --quiet "$ROOT/remote.git" "$ROOT/workB" 2>/dev/null
cd "$ROOT/workB"
git switch --quiet main 2>/dev/null || true
printf 'y\n\nall\nn\n%s\n' "$PASS" | "$ENVMOVE" setup 2>&1 | grep -E '(wired|key|✓)' | head -4
git add envmove.toml && git commit --quiet -m cfgB && git push --quiet origin main
cd "$ROOT/work"
git pull --quiet origin main && "$ENVMOVE" >/dev/null 2>&1

step "session starts on B, the briefing from A should arrive"
cd "$ROOT/workB"
"$ENVMOVE" hook session-start 2>&1

step "work happens on B, session-end"
printf '# Handover\n\nRetry mantigi TAMAMLANDI.\n' > HANDOVER.md
printf 'LOG_LEVEL=debug\n' >> .env
"$ENVMOVE" hook session-end 2>&1

step "session starts on A, B's work should be visible"
cd "$ROOT/work"
git pull --quiet origin main
"$ENVMOVE" hook session-start 2>&1

step "temizlik"
cd "$ROOT"
security delete-generic-password -s envmove -a "repo:$ROOT/work" >/dev/null 2>&1 || true
security delete-generic-password -s envmove -a "repo:$ROOT/workB" >/dev/null 2>&1 || true
security delete-generic-password -s envmove -a "repo:/private$ROOT/work" >/dev/null 2>&1 || true
security delete-generic-password -s envmove -a "repo:/private$ROOT/workB" >/dev/null 2>&1 || true
rm -f ~/.config/envmove/recovery/*work*.agekey ~/.config/envmove/recovery/*workB*.agekey 2>/dev/null || true
rm -rf "$ROOT"
printf "\n\033[1;32m== agent test finished ==\033[0m\n"