#!/bin/bash
# Verifies that the suite leaves global git configuration alone.
#
# This guard exists because of a bug. The test scripts once ran
# `git config --global user.name`, so running the suite silently replaced the
# machine owner's name and email and every commit afterwards was attributed to
# "envmove test". It was noticed in a real project.
#
# The scripts now pass identity through the environment instead, but this guard
# catches the mistake coming back from anywhere.
set -euo pipefail

snapshot() {
	git config --global --list 2>/dev/null | sort || true
}

BEFORE=$(snapshot)
status=0

for scenario in e2e recover agent restore example; do
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

AFTER=$(snapshot)

if [ "$BEFORE" != "$AFTER" ]; then
	echo "✗ global git config changed:"
	diff <(echo "$BEFORE") <(echo "$AFTER") || true
	exit 1
fi

if [ -n "$(git config --global --get user.name || true)" ] &&
	[ "$(git config --global --get user.name)" = "envmove test" ]; then
	echo "✗ global user.name was overwritten with the test identity"
	exit 1
fi

echo "✓ global git config untouched"
exit $status