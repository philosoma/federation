#!/usr/bin/env bash
# Back out one goatbuilder peering: its tunnel, SPIRE federation, edge router,
# republisher, forwarder mapping and core admission. FED_BACKOUT_DRY=1 only
# surveys. Other peerings are left as they were. New in goatbuilder 0.5.1 and
# not yet run on real hosts: survey first.
. "$FED_SCRIPTS/lib.sh"
STEPS=2
setup_home
sync_repos
assemble
if [ "${FED_BACKOUT_DRY:-1}" = 1 ]; then
	step "goatbuilder federate backout $THEM --dry-run (changes nothing)"
	gb backout "$THEM" --dry-run || die "The survey failed. Open the log."
	echo "::note Survey only: nothing was removed. Read the log, then back out if it lists only $THEM's resources."
	exit 0
fi
step "goatbuilder federate backout $THEM"
gb backout "$THEM" --yes; rc=$?
r=$GBW/backout/result.json
[ -f "$r" ] && python3 -c 'import json,sys; r=json.load(open(sys.argv[1])); print("converged:", r.get("converged")); print("left for review:", r.get("left_to_configuration"))' "$r"
[ $rc -eq 0 ] || die "Backout stopped (exit $rc). The log names the GB code; nothing after it ran."
echo "::note Backed out. Still to remove by review: the peerings: entry in goatbuilder.yaml, the forwarder entry, $THEM in peerings.yml, and its line in the release file."
