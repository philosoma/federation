#!/usr/bin/env bash
# Phase 14: our half of acceptance. The verdict is read from the evidence:
# `test` exits 1 for anything but PASS, INCOMPLETE included.
. "$FED_SCRIPTS/lib.sh"
STEPS=2
setup_home
use_plan_tree
# Every other peering is checked for leaks too: name their work directories.
others=()
for d in "$FED_STATE"/gb/* "$FED_STATE"/work/*; do
	[ -f "$d/agreement.json" ] && [ "$(basename "$d")" != "$THEM" ] && others+=("$d")
done
step "goatbuilder federate test $THEM: phase 14"
r=$GBW/evidence/acceptance/result.json
rm -f "$r"
FED_EXISTING_WORKS=$(IFS=:; echo "${others[*]:-}") gb_run test "$THEM" --yes || true
[ -f "$r" ] || gb_die "Acceptance didn't write a result. Open the log."
read -r verdict pass fail other < <(python3 -c '
import json, sys
r = json.load(open(sys.argv[1])); c = r.get("counts", {})
p, f = c.get("PASS", 0), c.get("FAIL", 0)
print(r.get("verdict", "?"), p, f, sum(c.values()) - p - f)' "$r")
case $verdict in
PASS) summary "PASS: $pass checks passed" ;;
INCOMPLETE)
	summary "our half: $pass passed, $other not run yet"
	echo "::note Our checks ran: $pass passed, none failed, $other couldn't run yet (they need probes or the partner's half). Compare with ${FED_NAME:-$THEM}, then confirm."
	;;
*) gb_die "Acceptance: $verdict ($fail failed). The table shows which check; the reason is read on the receiving side's router log." ;;
esac
