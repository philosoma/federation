#!/usr/bin/env bash
# Phases 1-5 (goatbuilder): check, measure, sign, exchange. Stops at phase 5
# with the codes to read aloud.
. "$FED_SCRIPTS/lib.sh"
STEPS=3
setup_home
sync_repos
assemble
step "goatbuilder federate $THEM (phases 1 to 5)"
out=$(gb "$THEM" --yes --confirm 2>&1); rc=$?
echo "$out"
if echo "$out" | grep -q "GB-192"; then
	our_key=$(echo "$out" | grep -oE "our verifying key [A-Za-z0-9+/=]+" | head -1 | awk '{print $4}')
	our_ca=$(echo "$out" | grep -oE "our CA fingerprint [^ ,;]+" | head -1 | awk '{print $4}')
	their_ca=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["identity"]["ca_fingerprint"])' "$GBW/theirs.canonical.json" 2>/dev/null)
	[ -n "$our_key" ] && show our_key "$(groups4 "$our_key")"
	[ -n "$our_ca" ] && show our_ca "$(groups4 "${our_ca#sha256:}")"
	[ -n "$their_ca" ] && show their_ca "$their_ca"
	[ -n "$their_ca" ] || waiting "${FED_NAME:-$THEM}'s package isn't in the directory yet. Run this again after they've run theirs."
	exit 0
fi
[ $rc -eq 0 ] || die "goatbuilder stopped (exit $rc). The log names the phase and the GB code."
die "goatbuilder didn't stop at phase 5 for the codes. Open the log."
