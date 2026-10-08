#!/bin/bash
# Recovery scenario: access to the keychain is gone (macOS reinstalled, laptop lost).
set -e
ROOT=/tmp/envmove-recover
ENVMOVE=/tmp/envmove
SECRET_PASS='kurtarma-sifresi-2026-bunu-1passworda-koy'

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
git clone --quiet "$ROOT/remote.git" "$ROOT/work"
cd "$ROOT/work"
git checkout --quiet -b main
printf '.env\nHANDOVER.md\n' > .gitignore
printf 'x\n' > main.py
git add -A && git commit --quiet -m init && git push --quiet -u origin HEAD:refs/heads/main

printf 'SECRET=prod-ish\n' > .env
printf '# Handover\nsirada: billing\n' > HANDOVER.md

# cevaplar: repoya dahil et / branch varsayilan / hepsi / sifre uretme / sifre gir
step "setup"
printf 'y\n\nall\nn\n%s\n' "$SECRET_PASS" | "$ENVMOVE" setup 2>&1 | tail -12

git add envmove.toml && git commit --quiet -m cfg && git push --quiet origin main

step "was the recovery copy written"
ls -l ~/.config/envmove/recovery/ | tail -2
echo "--- is the content encrypted (first bytes) ---"
head -c 30 ~/.config/envmove/recovery/*.agekey; echo

step "simulate: the keychain entry is deleted"
security delete-generic-password -s envmove -a "repo:$ROOT/work" >/dev/null 2>&1 || true
security delete-generic-password -s envmove -a "repo:/private$ROOT/work" >/dev/null 2>&1 || true
echo "deleted"

step "envmove ne diyor"
"$ENVMOVE" 2>&1 || true

step "doctor"
"$ENVMOVE" doctor 2>&1 | head -12

step "recover: wrong passphrase"
printf 'wrong-passphrase\n' | "$ENVMOVE" recover 2>&1 || true

step "recover: correct passphrase"
printf '%s\n' "$SECRET_PASS" | "$ENVMOVE" recover 2>&1

step "envmove tekrar calisiyor mu"
"$ENVMOVE" 2>&1

step "doctor again"
"$ENVMOVE" doctor 2>&1 | head -12

step "temizlik"
security delete-generic-password -s envmove -a "repo:$ROOT/work" >/dev/null 2>&1 || true
rm -f ~/.config/envmove/recovery/$(basename $ROOT)_work.agekey 2>/dev/null || true
ls ~/.config/envmove/recovery/ 2>/dev/null | tail -2
rm -rf "$ROOT"

printf "\n\033[1;32m== recovery test finished ==\033[0m\n"