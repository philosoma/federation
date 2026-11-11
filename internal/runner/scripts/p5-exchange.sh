#!/usr/bin/env bash
# Phase 5: publish ours to the directory, fetch theirs, print what to read aloud.
. "$FED_SCRIPTS/lib.sh"
STEPS=1
setup_home
TREE=$(cat "$W/tree" 2>/dev/null) || die "Run phase 2-4 first."
[ -s "$W/ours.canonical.json" ] || die "No package of ours yet. Run phase 2-4."

# Saltlick holds one package per country: fetch theirs fresh every run, in
# case they republished.
keep_old theirs.canonical.json theirs-ca.pem keys.txt
step "Directory: $FED_DIRECTORY_URL"
out=$(python3 -I "$TREE/ops/federation/standup/directory.py" --url "$FED_DIRECTORY_URL" \
	--ours "$W/ours.canonical.json" --key "$SEED" --ca "$W/ours-ca.pem" \
	--their-id "$THEM" --theirs "$W/theirs.canonical.json" --keyring "$W/keys.txt" --work "$W" 2>&1)
echo "$out"

# directory.py prefixes each code with "<n> groups: "; the page shows the code alone.
nogroups() { sed -E 's/^[0-9]+ groups: //'; }
ours_key=$(echo "$out" | sed -n 's/.*our verifying key, to read aloud: //p' | tail -1 | nogroups)
ours_ca=$(echo "$out" | sed -n 's/.*our CA fingerprint, to read aloud: //p' | tail -1 | nogroups)
[ -n "$ours_key" ] || die "Our package wasn't published. See the log."
echo "$ours_key" | tr -d ' ' >"$W/our-key.txt"
show our_key "$ours_key"
[ -n "$ours_ca" ] && show our_ca "$ours_ca"

if [ ! -s "$W/theirs.canonical.json" ]; then
	waiting "Ours is published. Their package isn't on the directory yet: run phase 5 again once $THEM has published."
fi
their_ca=$(echo "$out" | sed -n 's/.*their CA fingerprint: *//p' | tail -1 | nogroups)
their_key=$(echo "$out" | sed -n 's/.*their verifying key ([^)]*): //p' | tail -1 | nogroups)
[ -n "$their_ca" ] && show their_ca "$their_ca"
[ -n "$their_key" ] && show their_key "$their_key"
# Phase 6 binds this key once both sides have confirmed the codes.
echo "$their_key" | tr -d ' ' >"$W/their-key.txt"
if echo "$out" | grep -E '^\s+FAIL ' >/dev/null; then
	die "The compatibility report has a FAIL row. Settle it with $THEM before the call."
fi
echo "::note Confirm with ${FED_NAME:-$THEM} that each code is correct: they check ours, we check theirs. Then tick both below."
