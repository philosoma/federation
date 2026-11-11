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
if echo "$GB_OUT" | grep -q "GB-192"; then
	our_key=$(echo "$GB_OUT" | grep -oE "our verifying key [A-Za-z0-9+/=]+" | head -1 | awk '{print $4}')
	our_ca=$(echo "$GB_OUT" | grep -oE "our CA fingerprint [^ ,;]+" | head -1 | awk '{print $4}')
	their_ca=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["identity"]["ca_fingerprint"])' "$GBW/theirs.canonical.json" 2>/dev/null) || their_ca=
	[ -n "$their_ca" ] || waiting "${FED_NAME:-$THEM}'s package isn't on the directory yet. Ask them to run their exchange, then run this again."
	[ -n "$our_key" ] && [ -n "$our_ca" ] || gb_die "goatbuilder stopped for the read-back but didn't print our codes. Open the log."
	show our_key "$(groups "$our_key")"
	show our_ca "$(groups "${our_ca#sha256:}")"
	show their_ca "$(groups "${their_ca#sha256:}")"
	show raw_their_ca "$their_ca"
	echo "$GB_OUT" | grep -qE '(^|[^A-Z])FAIL([^A-Z]|$)' && echo "::note The compatibility report has a FAIL row: read it in the log before you confirm anything."
	summary "packages exchanged; codes confirmed on the call"
	exit 0
fi
echo "$GB_OUT" | grep -qiE "not (yet )?(in|on) the directory|no entry|404" && waiting "${FED_NAME:-$THEM}'s package isn't on the directory yet. Ask them to run their exchange, then run this again."
gb_die "goatbuilder stopped before the exchange finished (exit $rc). The log names the phase and what to fix."
