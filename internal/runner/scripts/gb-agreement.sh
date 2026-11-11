#!/usr/bin/env bash
# Phases 6-9 (goatbuilder): compose and verify the agreement. Stops at phase 6
# with the digest to read aloud.
. "$FED_SCRIPTS/lib.sh"
STEPS=3
: "${FED_THEIR_KEY:?Confirm their key at phase 5 first}" "${FED_THEIR_CA:?Run phase 5 first}"
setup_home
sync_repos
assemble
step "goatbuilder federate $THEM (phases 5 to 6)"
out=$(gb "$THEM" --yes --partner-key "$FED_THEIR_KEY" --partner-ca-fingerprint "$FED_THEIR_CA" 2>&1); rc=$?
echo "$out"
d=$(echo "$out" | grep -oE "read sha256:[0-9a-f]+" | head -1 | sed 's/^read //')
[ -n "$d" ] || d=$(echo "$out" | gb_value "agreement digest sha256:" | sed 's/^/sha256:/')
if [ -n "$d" ] && [ "$d" != "sha256:" ]; then
	show digest "$d"
	exit 0
fi
echo "$out" | grep -q "GB-191" && die "The key or fingerprint doesn't match what they published (GB-191). Check them with ${FED_NAME:-$THEM} and record phase 5 again."
die "goatbuilder stopped (exit $rc) without a digest. Open the log."
