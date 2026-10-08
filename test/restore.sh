#!/bin/bash
# the restore and rotate-key scenario
set -e
ROOT=/tmp/envmove-restore
ENVMOVE=/tmp/envmove
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
set +e

git init --quiet --bare "$ROOT/remote.git"
git clone --quiet "$ROOT/remote.git" "$ROOT/work" 2>/dev/null
cd "$ROOT/work"
git checkout --quiet -b main
printf '.env\nHANDOVER.md\n' > .gitignore
printf 'x\n' > main.py
printf 'SECRET=genel\n' > .env
printf 'HANDOVER v1: planlama\n' > HANDOVER.md
git add .gitignore main.py && git commit --quiet -m init && git push --quiet -u origin HEAD:refs/heads/main

step "setup (the config is published automatically when asked: y)"
printf 'y\n\nall\nn\n%s\ne\n' "$PASS" | "$ENVMOVE" setup 2>&1 | tail -10

step "v1, push"
"$ENVMOVE" >/dev/null 2>&1
git log --oneline envmove | head -1

step "v2: .env and HANDOVER changed"
printf 'SECRET=genel\nEXTRA=2\n' > .env
printf 'HANDOVER v2: yarida kaldi\n' > HANDOVER.md
"$ENVMOVE" >/dev/null 2>&1
GOOD=$(git log --format=%H -1 envmove)
echo "the good state: ${GOOD:0:7}"

step "v3: we broke things"
printf 'SECRET=BOZUK\nGOMULU=yes\n' > .env
printf 'HANDOVER v3: her sey bozuldu\n' > HANDOVER.md
"$ENVMOVE" >/dev/null 2>&1
BAD=$(git log --format=%H -1 envmove)
echo "the broken state: ${BAD:0:7}"
echo "--- the broken .env ---"; cat .env

step "restore: the snapshot list"
"$ENVMOVE" restore 2>&1 <<< "2"
echo "--- resulting .env ---"; cat .env
echo "--- resulting HANDOVER ---"; cat HANDOVER.md

step "restore 1h (relative time resolution)"
printf 'SECRET=BOZUK2\n' > .env
"$ENVMOVE" restore 1h --force 2>&1 | tail -4
echo "--- .env ---"; cat .env

step "restore: unpushed work is protected"
printf 'YENI_KOD=silinmesin\n' >> .env
"$ENVMOVE" restore 2h 2>&1 <<< "1"
echo "--- is .env still intact ---"; cat .env

step "rotate-key (with the recovery passphrase)"
printf '%s\n' "$PASS" | "$ENVMOVE" rotate-key 2>&1 | tail -12

step "doctor"
"$ENVMOVE" doctor 2>&1 | head -10

step "temizlik"
security delete-generic-password -s envmove -a "repo:$ROOT/work" >/dev/null 2>&1
security delete-generic-password -s envmove -a "repo:/private$ROOT/work" >/dev/null 2>&1
rm -f ~/.config/envmove/recovery/*restore*.agekey 2>/dev/null
rm -rf "$ROOT"
printf "\n\033[1;32m== restore/rotate testi bitti ==\033[0m\n"