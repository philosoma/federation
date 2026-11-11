#!/usr/bin/env bash
# Phase 14: our responder on the core, then acceptance.
. "$FED_SCRIPTS/lib.sh"
STEPS=2
setup_home
TREE=$(cat "$W/tree" 2>/dev/null) || die "Run the earlier phases first."
rt=$TREE/ops/federation/roundtrip

step "Starting our responder on the core (answers $THEM's probes)"
scp -q "$rt/responder.py" "$rt/fedcheck.sh" "$CORE:/tmp/" || die "Couldn't copy the responder to the core."
ssh_core "pid=\$(cat /tmp/responder-$THEM.pid 2>/dev/null); [ -n \"\$pid\" ] && kill \$pid 2>/dev/null; nohup python3 /tmp/responder.py from/$THEM release/REL-ALL >/tmp/responder-$THEM.log 2>&1 & echo \$! >/tmp/responder-$THEM.pid; sleep 1; ps -p \$(cat /tmp/responder-$THEM.pid) >/dev/null && echo responder running"

step "acceptance.sh"
cd "$TREE/ops/federation/standup"
FED_SSH_OURS=$CORE FED_OURS_REST=http://127.0.0.1:8000 timeout 300 ./acceptance.sh "$W/agreement.json" --as "$US" || true

# acceptance.sh exits non-zero unless every check ran, and in production half
# of them can't: they need the partner's core (BLOCKED). What we can prove is our
# round trip, with nothing failing; the partner proves theirs on their side.
read -r pass fail blocked < <(python3 -c '
import json, sys
c = json.load(open(sys.argv[1])).get("counts", {})
print(c.get("PASS", 0), c.get("FAIL", 0), c.get("BLOCKED", 0))' "$W/evidence/acceptance/result.json" 2>/dev/null || echo "0 1 0")
rt=$(python3 -c '
import json, sys
r = json.load(open(sys.argv[1]))
print(sum(1 for c in r.get("checks", []) if str(c.get("name", "")).startswith("roundtrip/") and str(c.get("verdict", "")).upper() == "PASS"))' "$W/evidence/acceptance/result.json" 2>/dev/null || echo 0)
if [ "$fail" -eq 0 ] && [ "$rt" -ge 1 ]; then
	ms=$(python3 -c '
import json, re, sys
r = json.load(open(sys.argv[1]))
for c in r.get("checks", []):
    if str(c.get("name", "")).startswith("roundtrip/") and str(c.get("verdict", "")).upper() == "PASS":
        m = re.search(r"(\d+)ms", c.get("reason", ""))
        print(m.group(1) if m else ""); break' "$W/evidence/acceptance/result.json" 2>/dev/null)
show acceptance "our round trip passed${ms:+ in ${ms} ms} ($pass passed, 0 failed; $blocked need ${FED_NAME:-$THEM}'s side)"
	echo "::note Our round trip passed. Confirm with ${FED_NAME:-$THEM} that theirs passed too, then tick below."
	exit 0
fi
waiting "No round trip yet. Ask ${FED_NAME:-$THEM}: is their responder running (responder.py from/$US release/REL-ALL), is their republisher running, and is goat_zenoh_edge_core_cn set on their edge? Then run phase 14 again."
