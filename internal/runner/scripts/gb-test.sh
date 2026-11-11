#!/usr/bin/env bash
# Phase 14 (goatbuilder): our half of acceptance. The verdict is read from the
# evidence: `test` exits 1 for anything but PASS, INCOMPLETE included.
. "$FED_SCRIPTS/lib.sh"
STEPS=2
setup_home
use_plan_tree
# Every other peering is checked for leaks too: their work directories.
others=()
for d in "$FED_STATE"/gb/* "$FED_STATE"/work/*; do
	[ -f "$d/agreement.json" ] && [ "$(basename "$d")" != "$THEM" ] && others+=("$d")
done
step "goatbuilder federate test $THEM (phase 14)"
FED_EXISTING_WORKS=$(IFS=:; echo "${others[*]}") gb test "$THEM" --yes || true
r=$GBW/evidence/acceptance/result.json
[ -f "$r" ] || die "No acceptance result was written. Open the log."
read -r verdict counts < <(python3 -c 'import json,sys; r=json.load(open(sys.argv[1])); print(r.get("verdict","?"), json.dumps(r.get("counts",{})))' "$r")
show acceptance "$verdict $counts"
case $verdict in
PASS) exit 0 ;;
INCOMPLETE) echo "::note INCOMPLETE: our checks are in; the partner's half comes in through a shared acceptance session (federation-acceptance.md §6). Confirm with them, then tick below."; exit 0 ;;
*) die "Acceptance: $verdict. The evidence names the check: $r" ;;
esac
