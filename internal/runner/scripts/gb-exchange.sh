#!/usr/bin/env bash
# Phases 1-5: check, measure, sign, exchange through the directory. Stops at
# phase 5 with the codes both operators read aloud.
. "$FED_SCRIPTS/lib.sh"
STEPS=3
setup_home
sync_repos
assemble
step "goatbuilder federate $THEM: phases 1 to 5"
rc=0; gb_run "$THEM" --yes --confirm || rc=$?
# Phase 2 may have made this peering's carrier keys: keep them in the config.
save_carrier_keys
git -C "$CFG" add -A && git -C "$CFG" diff --cached --quiet || push_config "Carrier public keys for the $THEM peering"
# goatbuilder prints our two codes already grouped for the read-back.
pick() { echo "$GB_OUT" | sed -n "s/.*$1//p" | head -1 | sed 's/[[:space:]]*$//'; }
our_key=$(pick 'our verifying key, to read aloud: ')
our_ca=$(pick 'our CA fingerprint, to read aloud: ')
[ -n "$our_key" ] && show our_key "$our_key"
[ -n "$our_ca" ] && show our_ca "$our_ca"
if echo "$GB_OUT" | grep -q "GB-192"; then
	their_ca=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["identity"]["ca_fingerprint"])' "$GBW/theirs.canonical.json" 2>/dev/null) || their_ca=
	[ -n "$their_ca" ] || waiting "Our package is published. ${FED_NAME:-$THEM}'s isn't on the directory yet: ask them to run their exchange, then run this again."
	[ -n "$our_key" ] && [ -n "$our_ca" ] || gb_die "goatbuilder stopped for the read-back but didn't print our codes. Open the log."
	show their_ca "$(groups "${their_ca#sha256:}")"
	show raw_their_ca "$their_ca"
	if echo "$GB_OUT" | grep -qE '^ +FAIL '; then echo "::note The compatibility report has a FAIL row: read it in the log before you confirm anything."; fi
	summary "packages exchanged; codes confirmed on the call"
	exit 0
fi
if echo "$GB_OUT" | grep -qE "no entry for|HTTP 404"; then waiting "Our package is published. ${FED_NAME:-$THEM}'s isn't on the directory yet: ask them to run their exchange, then run this again."; fi
gb_die "goatbuilder stopped before the exchange finished (exit $rc). The log names the phase and what to fix."
