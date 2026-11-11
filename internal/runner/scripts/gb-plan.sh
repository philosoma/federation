#!/usr/bin/env bash
# Plan (goatbuilder): phases 6-9 with the confirmed digest, then the bounded
# plan and what our core will admit. Changes no host.
. "$FED_SCRIPTS/lib.sh"
STEPS=5
: "${FED_THEIR_KEY:?}" "${FED_THEIR_CA:?}" "${FED_DIGEST:?Confirm the digest first}"
setup_home
sync_repos
step "What $THEM may read on our core (its own prefix and the acceptance keys)"
python3 "$FED_SCRIPTS/fedcfg.py" gb-reads --repo "$CFG" --them "$THEM" --us "$US" --site "$FED_SITE" || die "Couldn't write $THEM's reads."
push_config "What $THEM reads on our core: its own prefix (Federation)"
assemble
step "goatbuilder federate $THEM (phases 6 to 9)"
gb "$THEM" --yes --partner-key "$FED_THEIR_KEY" --partner-ca-fingerprint "$FED_THEIR_CA" --agreement-digest "$FED_DIGEST" \
	|| die "Phases 6 to 9 stopped. The log names the phase and the GB code."
step "goatbuilder federate plan $THEM"
out=$(gb plan "$THEM" 2>&1); rc=$?
echo "$out"
[ $rc -eq 0 ] || die "The plan is blocked: read its conflicts and readiness rows in the log."
h=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["hash"])' "$GBW/plan.json" 2>/dev/null)
[ -n "$h" ] || die "No plan hash in $GBW/plan.json."
python3 - "$GBW/plan.json" <<'PY'
import json, sys
p = json.load(open(sys.argv[1]))
print("\nconflicts:", p.get("conflicts") or "none")
for r in p.get("resources", []):
    print(f"  owns {r.get('kind')} {r.get('name')} on {r.get('on')}")
for r in p.get("restarts", []):
    print("  restarts", r)
PY
step "What our core will admit"
out=$(gb deploy "$THEM" --yes --approve-plan "$h" --confirm 2>&1); rc=$?
echo "$out"
a=$(echo "$out" | grep -oE "Approval digest: sha256:[0-9a-f]+" | head -1 | sed 's/^Approval digest: //')
[ -n "$a" ] || a=$(echo "$out" | grep -oE "approve-admission sha256:[0-9a-f]+" | head -1 | awk '{print $2}')
[ -n "$a" ] || die "No admission digest in goatbuilder's output (exit $rc). Open the log."
show plan_hash "$h"
show admission_digest "$a"
echo "::note Read the plan and the admission in the log, then approve both below."
