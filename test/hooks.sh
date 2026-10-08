#!/bin/bash
# End to end: the git hooks, which are the whole mechanism.
#
# The unit tests check that the hook script is well formed. This checks that a real
# `git commit` and a real `git push` in a real repository do what the README claims,
# because that is the part a user experiences and the part nothing else exercises.
set -e

ROOT=/tmp/envmove-hooks
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ENVMOVE="${ENVMOVE:-$SCRIPT_DIR/../bin/envmove}"
rm -rf "$ROOT"; mkdir -p "$ROOT"
rm -f ~/.config/envmove/recovery/*.agekey 2>/dev/null || true

step() { printf "\n\033[1;36m== %s\033[0m\n" "$1"; }
fail() { printf "\033[1;31mFAIL: %s\033[0m\n" "$1"; exit 1; }

# Per command, never `git config --global`. See the note in test/e2e.sh: a test script
# that writes the global config silently renames the machine owner.
export GIT_AUTHOR_NAME="envmove test"
export GIT_AUTHOR_EMAIL="test@envmove.local"
export GIT_COMMITTER_NAME="envmove test"
export GIT_COMMITTER_EMAIL="test@envmove.local"

step "bare remote kuruluyor"
git init --quiet --bare "$ROOT/remote.git"
git clone --quiet "$ROOT/remote.git" "$ROOT/A" 2>/dev/null
cd "$ROOT/A"
git checkout --quiet -b main 2>/dev/null || git switch --quiet -c main
printf 'node_modules/\n.env\nHANDOVER.md\n' > .gitignore
printf 'print("hello")\n' > main.py
git add .gitignore main.py
git commit --quiet -m "ilk commit"
git push --quiet -u origin main

step "setup: hook'lar yaziliyor"
printf 'SECRET_KEY=abc123\n' > .env
printf '# Handover\n\nyarim.\n' > HANDOVER.md
printf 'y\n\nall\nn\nkurtarma-sifresi-uzun-2026\n\n\n' | "$ENVMOVE" setup 2>&1 | grep -E 'hook|pushed|publish' || true

step "hook'lar yerinde mi"
for h in post-commit pre-push; do
  [ -f ".git/hooks/$h" ] || fail "$h not installed"
  grep -q 'command -v envmove' ".git/hooks/$h" \
    || fail "$h has no PATH fallback, a Homebrew upgrade will break it"
  echo "  ✓ $h"
done

step "the hook must not point only at a Cellar path"
# Homebrew deletes the previous version's directory on upgrade. A hook pinned to it
# breaks every commit the moment a new version lands, which is the bug this guards.
if grep -q 'for candidate in /opt/homebrew/Cellar' .git/hooks/post-commit; then
  fail "the hook tries the Cellar path before the stable one"
fi
echo "  ✓ stable path first"

step "a commit that touched only code must not publish anything"
"$ENVMOVE" >/dev/null 2>&1   # settle the state so the next commit starts clean
before=$(git rev-parse envmove)
printf 'print("hello again")\n' > main.py
git add -A
git commit --quiet -m "sadece kod"
after=$(git rev-parse envmove)
[ "$before" = "$after" ] || fail "a code-only commit moved the snapshot branch"
echo "  ✓ snapshot branch unchanged, nothing was published"

step "a commit that changes context must publish it"
# The code file has to change too. A commit that touches only a gitignored file never
# happens, so post-commit never fires — which is the hole `envmove watch` exists to
# cover. Here we want the ordinary case: code and context travel together.
printf '# Handover\n\nbitti.\n' > HANDOVER.md
printf 'SECRET_KEY=xyz789\n' >> .env
printf 'print("hello plus context")\n' > main.py
git add -A
git commit --quiet -m "kod + context"
after=$(git rev-parse envmove)
[ "$before" != "$after" ] || fail "a commit that changed context did not publish it"
echo "  ✓ snapshot branch moved"

step "the published content is the new one"
git show envmove:state.age > "$ROOT/state.age"
echo "  ✓ snapshot is ciphertext, $(wc -c < "$ROOT/state.age" | tr -d ' ') bytes"
git show envmove:state.age | grep -q 'xyz789' && fail "the secret is readable on the branch"
echo "  ✓ no plaintext on the branch"

step "the other machine receives it over a plain git push"
cd "$ROOT/A" && git push --quiet origin main
git clone --quiet "$ROOT/remote.git" "$ROOT/B"
cd "$ROOT/B"
git checkout --quiet main
# Both machines stay on main, which is how the tool is meant to be used: envmove.toml is
# committed code, so it travels the same way everything else does.
printf 'y\n\nall\nn\nkurtarma-sifresi-uzun-2026\n\n\n' | "$ENVMOVE" setup 2>&1 | grep -E 'publish|hook' || true

step "a newly added machine cannot read the old snapshot yet"
# The snapshot on the branch is encrypted for A only. B is now a recipient but the
# ciphertext does not name it yet, so B must be told to come back after A re-encrypts.
# The error has to say exactly that, or the person is left guessing.
out=$("$ENVMOVE" 2>&1 || true)
echo "$out" | grep -q 'cannot be decrypted' \
  || fail "expected a clear cannot-decrypt error, got: $out"
echo "$out" | grep -q 're-encrypt' \
  || fail "the error should say the other machine has to re-encrypt"
echo "  ✓ explained instead of failing cryptically"

step "A re-encrypts for the new recipient"
cd "$ROOT/A"
git pull --quiet --ff-only origin main
"$ENVMOVE" >/dev/null 2>&1
git push --quiet origin main

step "now B can read it"
cd "$ROOT/B"
git pull --quiet --ff-only origin main
"$ENVMOVE" >/dev/null 2>&1
grep -q 'bitti' HANDOVER.md || fail "B did not receive the handover note"
grep -q 'xyz789' .env || fail "B did not receive the new secret"
echo "  ✓ B has the context from A"

step "a stale hook must be reported by doctor, not silently trusted"
# Recreate the bug this scenario exists for: a hook pinned to a path that no longer
# exists, which is what a Homebrew upgrade leaves behind.
cat > .git/hooks/post-commit <<'HOOK'
#!/bin/sh
exec /opt/homebrew/Cellar/envmove/0.0.1/bin/envmove sync --hook --quiet
HOOK
chmod +x .git/hooks/post-commit
"$ENVMOVE" doctor 2>&1 | grep -q 'older envmove' \
  || fail "doctor did not notice a stale hook"
echo "  ✓ doctor flags the stale hook"

step "setup rewrites it"
printf 'y\n\nall\nn\nkurtarma-sifresi-uzun-2026\n\n\n' | "$ENVMOVE" setup >/dev/null 2>&1
grep -q 'command -v envmove' .git/hooks/post-commit || fail "setup did not rewrite the hook"
"$ENVMOVE" doctor 2>&1 | grep -q 'hook     ✓ post-commit' \
  || { "$ENVMOVE" doctor 2>&1 | grep hook; fail "doctor still unhappy after setup"; }
echo "  ✓ hook repaired"

step "a missing envmove must not make the repository uncommittable"
cp .git/hooks/post-commit "$ROOT/hook.bak"
cat > .git/hooks/post-commit <<'HOOK'
#!/bin/sh
self=""
for candidate in /nonexistent/envmove "$(command -v envmove 2>/dev/null)"; do
  if [ -x "$candidate" ]; then self="$candidate"; break; fi
done
if [ -z "$self" ]; then
  echo "envmove: not found, context is not syncing (this does not block your commit)" >&2
  exit 0
fi
exec "$self" sync --hook --quiet
HOOK
chmod +x .git/hooks/post-commit
printf 'print("still fine")\n' > main.py
git add -A
git commit --quiet -m "envmove yokken" 2>/dev/null \
  || fail "a missing envmove blocked the commit"
echo "  ✓ commit succeeded without envmove present"
cp "$ROOT/hook.bak" .git/hooks/post-commit

step "cleanup"
cd /
rm -rf "$ROOT"

printf "\n\033[1;32mhooks: all checks passed\033[0m\n"