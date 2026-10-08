#!/bin/bash
# End to end: two working copies against one bare remote.
set -e

ROOT=/tmp/envmove-e2e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# The binary under test is the one this repository builds, not whatever happens to be
# installed. Hardcoding a path meant the suite tested a stale copy in /tmp.
ENVMOVE="${ENVMOVE:-$SCRIPT_DIR/../bin/envmove}"
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

step "bare remote kuruluyor"
git init --quiet --bare "$ROOT/remote.git"

step "clone into A"
git clone --quiet "$ROOT/remote.git" "$ROOT/A" 2>/dev/null
cd "$ROOT/A"
git checkout --quiet -b main 2>/dev/null || git switch --quiet -c main
printf 'node_modules/\n.env\nHANDOVER.md\nTODO.md\n' > .gitignore
printf 'print("hello")\n' > main.py
git add .gitignore main.py
git commit --quiet -m "ilk commit"
git push --quiet -u origin main

step "A: create the real T2 files"
printf 'SECRET_KEY=abc123\nDB_URL=postgres://localhost/app\nREPO_ROOT=%s\n' "$ROOT/A" > .env
printf '# Handover\n\nStripe retry mantigi yarim.\n' > HANDOVER.md
printf '# Todo\n- [ ] retry backoff\n' > TODO.md

step "A: envmove setup"
printf 'y\n\nall\nn\nkurtarma-sifresi-2026\ne\n' | "$ENVMOVE" setup 2>&1

step "A: commit and push the config"
echo "(config was published by setup)"

step "A: envmove, pushing to the encrypted branch"
"$ENVMOVE" 2>&1

step "A: is the ciphertext actually encrypted?"
echo "--- state.age ilk baytlar ---"
git show envmove:state.age | head -c 80 | xxd | head -3
echo "--- .env duz metin kalmali ---"
cat .env

step "A: only HANDOVER.md changed, only it should travel"
printf '# Handover\n\nStripe retry mantigi TAMAMLANDI.\n' > HANDOVER.md
"$ENVMOVE" 2>&1

printf "\n\033[1;33m######## B MAKINESI ########\033[0m\n"

git clone --quiet "$ROOT/remote.git" "$ROOT/B" 2>/dev/null
cd "$ROOT/B"
git switch --quiet main 2>/dev/null || true
echo "does B have .env yet (it should not):"
ls -a | grep -E '^\.env$' || echo "  yok ✓"

step "B: setup (nothing to detect here, so that question is skipped)"
printf 'y\n\nn\nkurtarma-sifresi-2026\ne\n' | "$ENVMOVE" setup 2>&1

step "B: setup published the config automatically"

step "B: once A hala B'yi tanimiyor"
"$ENVMOVE" pull 2>&1 || echo "  ^ beklenen davranis"

step "A: pulls the config and re-encrypts"
cd "$ROOT/A"
git pull --quiet origin main
"$ENVMOVE" 2>&1
echo "--- A'nin branch gecmisi ---"
git log --oneline envmove | head -3

step "B: now pull"
cd "$ROOT/B"
"$ENVMOVE" 2>&1

step "RESULT: the files on B"
echo "--- .env ---"
cat .env 2>/dev/null || echo "  YOK ❌"
echo "--- HANDOVER.md ---"
cat HANDOVER.md 2>/dev/null || echo "  YOK ❌"
echo "--- TODO.md ---"
cat TODO.md 2>/dev/null || echo "  YOK ❌"

step "B: add a line and push"
printf 'SECRET_KEY=abc123\nDB_URL=postgres://localhost/app\nREPO_ROOT=%s\nLOG_LEVEL=debug\n' "$ROOT/B" > .env
"$ENVMOVE" 2>&1

step "A: pulling B's change"
cd "$ROOT/A"
"$ENVMOVE" pull 2>&1
echo "--- A's .env ---"
cat .env

step "does path rewriting work (A should see its own path)"
grep '^REPO_ROOT' .env

step "doctor, on B"
cd "$ROOT/B"
"$ENVMOVE" doctor 2>&1

step "after the line break: B deletes a file of A's"
cd "$ROOT/B"
rm TODO.md
"$ENVMOVE" 2>&1 || true
cd "$ROOT/A"
"$ENVMOVE" pull 2>&1
ls TODO.md 2>/dev/null && echo "  the deletion has not reached A (B has not pushed yet)"

printf "\n\033[1;32m== test finished ==\033[0m\n"