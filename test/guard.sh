#!/bin/bash
# Verifies that the suite leaves the machine alone.
#
# This guard exists because of a bug, twice.
#
# First: the test scripts once ran `git config --global user.name`, so running the suite
# silently replaced the machine owner's name and email and every commit afterwards was
# attributed to "envmove test". It was noticed in a real project.
#
# Second, the same shape of mistake: the scripts cleared
# ~/.config/envmove/recovery with a wildcard. That directory holds the passphrase-wrapped
# copy of each machine's private key, the only way back after a login keychain loss, and
# the wildcard deleted it for every real repository on the machine. It was noticed because
# a real repository stopped being recoverable.
#
# Both are the same mistake: a test script reaching outside its sandbox and changing
# something the machine owner depends on. Both had to be found in production.
#
# The scripts now pass identity through the environment and keep recovery copies under
# their own temporary directory. This guard catches either mistake coming back from
# anywhere, including from a scenario that does not exist yet.
set -euo pipefail

RECOVERY_DIR="${HOME}/.config/envmove/recovery"

snapshot_git_config() {
	git config --global --list 2>/dev/null | sort || true
}

# Fingerprints rather than contents: the suite is allowed to create and delete its own
# files under this directory, but not to touch anyone else's.
snapshot_recovery() {
	[ -d "$RECOVERY_DIR" ] || return 0
	find "$RECOVERY_DIR" -name '*.agekey' -type f 2>/dev/null |
		while read -r f; do
			printf '%s %s\n' "$(shasum -a 256 "$f" | cut -d' ' -f1)" "$f"
		done | sort || true
}

BEFORE_GIT=$(snapshot_git_config)
BEFORE_RECOVERY=$(snapshot_recovery)

status=0
for scenario in e2e recover agent restore example hooks; do
	script="$(dirname "$0")/$scenario.sh"
	echo "::group::$scenario"
	if ! bash "$script" >/dev/null 2>&1; then
		echo "  ✗ $scenario failed"
		status=1
	else
		echo "  ✓ $scenario"
	fi
	echo "::endgroup::"
done

AFTER_GIT=$(snapshot_git_config)
AFTER_RECOVERY=$(snapshot_recovery)

if [ "$BEFORE_GIT" != "$AFTER_GIT" ]; then
	echo "✗ global git config changed:"
	diff <(echo "$BEFORE_GIT") <(echo "$AFTER_GIT") || true
	exit 1
fi

if [ -n "$(git config --global --get user.name || true)" ] &&
	[ "$(git config --global --get user.name)" = "envmove test" ]; then
	echo "✗ global user.name was overwritten with the test identity"
	exit 1
fi

echo "✓ global git config untouched"

if [ "$BEFORE_RECOVERY" != "$AFTER_RECOVERY" ]; then
	echo "✗ recovery copies changed:"
	diff <(echo "$BEFORE_RECOVERY") <(echo "$AFTER_RECOVERY") || true
	echo "  A recovery copy is the only way back after a keychain loss."
	echo "  The suite must set ENVMOVE_RECOVERY_DIR rather than clearing this directory."
	exit 1
fi

if [ -z "$BEFORE_RECOVERY" ] && [ -z "$AFTER_RECOVERY" ]; then
	# Nothing here to protect, so the check above proves nothing. Say so rather than
	# reporting a green tick for a guard that never had anything to guard.
	echo "ℹ no recovery copies exist on this machine, so that check was vacuous"
fi

echo "✓ recovery copies untouched"
exit $status