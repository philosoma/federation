#!/usr/bin/env bash
# Phase 6: compose the agreement against the keys confirmed on the call. Stops
# with the digest both operators read aloud.
. "$FED_SCRIPTS/lib.sh"
STEPS=3
: "${FED_THEIR_KEY:?Confirm their key in the exchange step first}" "${FED_THEIR_CA:?Run the exchange step first}"
setup_home
sync_repos
assemble
step "goatbuilder federate $THEM: phases 5 to 6"
rc=0; gb_run "$THEM" --yes --partner-key "$FED_THEIR_KEY" --partner-ca-fingerprint "$FED_THEIR_CA" || rc=$?
d=$(echo "$GB_OUT" | grep -oE "read sha256:[0-9a-f]{64}" | head -1 | sed 's/^read //') || d=
if [ -z "$d" ] && [ -f "$GBW/agreement.json" ] && echo "$GB_OUT" | grep -q "GB-192"; then d="sha256:$(sha256sum "$GBW/agreement.json" | awk '{print $1}')"; fi
if [ -n "$d" ]; then
	# An agreement can compose and still federate nothing: check what it carries
	# before anyone reads its digest.
	verdict=$(python3 "$FED_SCRIPTS/offers.py" agreement "$GBW/agreement.json" "$US" "$THEM" "${FED_NAME:-$THEM}") || verdict=
	echo "$verdict"
	case $verdict in
	fail*) die "${verdict#fail }" ;;
	esac
	warn=$(echo "$verdict" | sed -n 's/^warn //p' | head -1)
	terms=$(echo "$verdict" | sed -n 's/^ok //p' | head -1)
	[ -n "$terms" ] && show terms "$terms"
	[ -n "$warn" ] && echo "::note $warn"
	show digest "$(groups "${d#sha256:}")"
	show raw_digest "$d"
	exp=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("expires",""))' "$GBW/agreement.json" 2>/dev/null) || exp=
	[ -n "$exp" ] && show expires "$exp"
	summary "agreement ${d:0:19}…${exp:+, ends $exp}"
	exit 0
fi
echo "$GB_OUT" | grep -q "GB-191" && gb_die "Their key or CA fingerprint doesn't match the package they published. Check both with ${FED_NAME:-$THEM}, then confirm the exchange step again."
echo "$GB_OUT" | grep -q "GB-193" && gb_die "The key you typed doesn't verify their package. Re-read it with ${FED_NAME:-$THEM} and confirm the exchange step again."
gb_die "goatbuilder stopped without an agreement (exit $rc). The log names the phase and what to fix."
