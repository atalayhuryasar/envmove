#!/bin/bash
# .env.example ureteci + otomatik config yayini
set -e
ROOT=/tmp/envmove-example
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
cat > .env.example <<'EOF'
# veritabani ayarlari
DB_HOST=localhost
DB_PORT=5432
EOF
git add -A && git commit --quiet -m init && git push --quiet -u origin HEAD:refs/heads/main

cat > .env <<EOF
# gizli: prod master sifresi
DB_HOST=db.internal
DB_PORT=5432
SECRET_KEY=hunter2
API_URL=https://api.example.com
EOF
printf 'HANDOVER v1\n' > HANDOVER.md

step "setup (config otomatik yayinlanacak: e)"
printf 'y\n\nall\nn\n%s\ne\n' "$PASS" | "$ENVMOVE" setup 2>&1 | grep -E '(published|wired|example|added|hook)' | head -8

step ".env.example uretildi mi"
cat .env.example

step "SAFETY: did a real value or a secret comment leak"
if grep -q 'hunter2' .env.example; then echo "  ❌ A REAL VALUE LEAKED"; else echo "  ✓ no real value"; fi
if grep -q 'prod master' .env.example; then echo "  ❌ GIZLI YORUM SIZDI"; else echo "  ✓ gizli yorum yok"; fi
if grep -q 'db.internal' .env.example; then echo "  ❌ A REAL HOST LEAKED"; else echo "  ✓ no real host"; fi
if grep -q 'veritabani' .env.example; then echo "  ✓ mevcut yorum korundu"; else echo "  ⚠ yorum kayboldu"; fi

step "config commit'lenmis mi (otomatik)"
cd "$ROOT/work"
git log --oneline -1
git status --porcelain envmove.toml | head -2
echo "(bos ise temiz)"

step "yeni key eklenince ornek guncelleniyor"
printf 'DB_HOST=db2\nDB_PORT=5432\nSECRET_KEY=hunter2\nAPI_URL=https://api.example.com\nRATE_LIMIT=60\n' > .env
"$ENVMOVE" 2>&1 | grep -E '(\.env\.example|↑)' | head -4
echo "--- guncel ornek ---"
cat .env.example

step "artik var olmayan key silinmis mi"
printf 'DB_HOST=db2\nSECRET_KEY=x\n' > .env
"$ENVMOVE" >/dev/null 2>&1
cat .env.example

step "doctor: warns when the example file is missing"
cd "$ROOT/work"
rm -f .env.example
git commit --quiet -am "ornek deleted"
"$ENVMOVE" doctor 2>&1 | tail -4

step "temizlik"
security delete-generic-password -s envmove -a "repo:$ROOT/work" >/dev/null 2>&1
security delete-generic-password -s envmove -a "repo:/private$ROOT/work" >/dev/null 2>&1
rm -f ~/.config/envmove/recovery/*example*.agekey 2>/dev/null
rm -rf "$ROOT"
printf "\n\033[1;32m== example test finished ==\033[0m\n"