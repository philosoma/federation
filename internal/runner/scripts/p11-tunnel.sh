#!/usr/bin/env bash
# Phase 11: open the transport the agreement chose.
. "$FED_SCRIPTS/lib.sh"
STEPS=4
setup_home
sync_repos
alloc
assemble
kind=$(python3 -c 'import json; print(json.load(open("'"$W"'/agreement.json"))["transport"]["kind"])') || die "No agreement. Run phases 6-9."
case "$kind" in
wireguard) pb=peering-wg.yml ;;
ipsec-fips) pb=peering-edge.yml ;;
*) die "The agreement chose '$kind', and there's no play for it. Don't substitute another transport." ;;
esac
play "${THEM}_edge" "$pb" || die "Bringing up the $kind link failed."

step "Waiting for a handshake on $IFACE"
for i in $(seq 1 24); do
	hs=$(ssh_edge "sudo -n wg show $IFACE latest-handshakes" | awk '{print $2}' | sort -n | tail -1)
	now=$(date +%s)
	if [ -n "$hs" ] && [ "$hs" -gt 0 ] && [ $((now - hs)) -lt 180 ]; then
		ssh_edge "sudo -n wg show $IFACE transfer"
		show handshake "$((now - hs))s ago"
		exit 0
	fi
	sleep 5
done
waiting "The link is up on our side but $THEM hasn't completed a handshake. Once their edge is up, run phase 11 again."
