#!/usr/bin/env bash
# Phases 6-9: compose, verify, mint the peering certificates, apply.
. "$FED_SCRIPTS/lib.sh"
STEPS=4
setup_home
TREE=$(cat "$W/tree" 2>/dev/null) || die "Run phase 2-4 first."
# Their key as fetched in phase 5 and confirmed with them (older records typed it in).
FED_THEIR_KEY=${FED_THEIR_KEY:-$(tr -d ' \n' <"$W/their-key.txt" 2>/dev/null)}
[ -n "$FED_THEIR_KEY" ] || die "No confirmed key for $THEM. Run phase 5 again."
ours=$(cat "$W/our-key.txt" 2>/dev/null) || die "Run phase 5 first."
cd "$W"
printf '# Confirmed with both sides, recorded in Federation\n%s=%s\n%s=%s\n' "$US" "$ours" "$THEM" "$FED_THEIR_KEY" >keys.txt

keep_old agreement.json pki applied
step "6. goat federate compose"
out=$(goat federate compose ours.canonical.json theirs.canonical.json --key "$US=$ours" --key "$THEM=$FED_THEIR_KEY" --out agreement.json 2>&1) \
	|| { echo "$out"; die "Compose refused. If it names a key, the key read on the call doesn't match their package."; }
echo "$out"
step "7. goat federate verify --as $US"
goat federate verify agreement.json --as "$US" || die "Verify refused the agreement from our side."

# The link subnet is the partner's if they sort first, so it can land on an
# address the edge already uses (another peering's link or carrier). Catch it
# here, before certificates are minted or anything is deployed.
. "$W/alloc.env" 2>/dev/null || true
link=$(python3 -c 'import json; print(json.load(open("agreement.json"))["transport"].get("link_subnet", ""))')
if [ -n "$link" ]; then
	addrs=$(ssh_edge "ip -o -4 addr show" | awk '{print $2, $4}') || die "Can't read the edge's addresses to check the link subnet."
	clash=$(echo "$addrs" | python3 -c '
import ipaddress, sys
net = ipaddress.ip_network(sys.argv[1], strict=False)
own = sys.argv[2]
for line in sys.stdin:
    p = line.split()
    if len(p) == 2 and p[0] != own:
        iface = ipaddress.ip_interface(p[1])
        if iface.ip in net or iface.network.overlaps(net):
            print(p[0], p[1])' "$link" "${IFACE:-}")
	if [ -n "$clash" ]; then
		echo "$clash"
		die "The agreed link subnet $link is already in use on the edge ($(echo "$clash" | head -1)). Free that address or recompose with $THEM on another subnet."
	fi
	echo "link subnet $link is free on the edge"
fi

digest=$(echo "$out" | grep -o 'sha256:[0-9a-f]\{64\}' | head -1)
[ -n "$digest" ] || digest="sha256:$(sha256sum agreement.json | cut -d' ' -f1)"
show digest "sha256: $(groups4 "${digest#sha256:}")"
expires=$(python3 -c 'import json,sys; a=json.load(open("agreement.json")); print(a.get("expires") or a.get("not_after") or "")' 2>/dev/null || true)
[ -n "$expires" ] && show expires "$expires"
echo "$digest" >digest.txt

step "8. Mint the peering certificates"
FED_CA_KEY=$CA_KEY FED_CA_CERT=$W/ours-ca.pem bash "$TREE/ops/federation/standup/mint-peering-leaves.sh" agreement.json pki --as "$US" \
	|| die "Minting the peering certificates failed."
ls -1 pki
step "9. goat federate apply"
goat federate apply agreement.json --as "$US" --edge-group "${THEM}_edge" --out applied --confirm || die "Apply refused."
find applied -type f | sort
echo "::note Confirm with ${FED_NAME:-$THEM} that their digest is the same as ours. Then tick below."
