#!/usr/bin/env bash
# Phase 1: declare the peering in our config, then the driver's preflight.
. "$FED_SCRIPTS/lib.sh"
STEPS=5
setup_home
sync_repos
alloc

[ "$FED_WE_LISTEN" != true ] || [ -n "$FED_PUBLIC_NAME" ] || die "We listen on this link, so set FED_PUBLIC_NAME, the forwarder's name partners dial."
step "Declaring the peering in the config overlay"
python3 "$FED_SCRIPTS/fedcfg.py" declare --repo "$CFG" --them "$THEM" --us "$US" --site "$FED_SITE" \
	--gb-environment "$FED_GB_ENVIRONMENT" --gb-site "$FED_GB_SITE" \
	--edge-name "$FED_EDGE_NAME" --edge-host "$FED_EDGE_HOST" --edge-user "$FED_EDGE_USER" \
	--core-name "$FED_CORE_NAME" --core-host "$FED_CORE_HOST" --core-user "$FED_CORE_USER" \
	--iface "$IFACE" --listen-port "$LISTEN_PORT" --edge-port "$EDGE_PORT" --we-listen "$FED_WE_LISTEN" \
	--public-name "$FED_PUBLIC_NAME" --carrier-port "$CARRIER_PORT" --carrier-iface "$CARRIER_IFACE" \
	--carrier-net "$CARRIER_NET" --carrier-local-port "$CARRIER_LOCAL_PORT" --metrics-port "$METRICS_PORT" \
	--waiver "$FED_FIPS_WAIVER"
git -C "$CFG" --no-pager diff --stat
push_config "Declare the peering with $THEM"

federation_toml
assemble

step "Checking we can reach the edge and the core"
ssh_edge 'hostname; sudo -n true && echo "sudo ok"' || die "Can't reach the edge as $EDGE with passwordless sudo."
ssh_core 'hostname; sudo -n true && echo "sudo ok"' || die "Can't reach the core as $CORE with passwordless sudo."

step "goatbuilder federate $THEM --to 1 (preflight)"
export FED_WORK=$W FED_DIRECTORY_URL FED_COUNTERPARTY_ID=$THEM FED_SIGNING_KEY=$SEED
export FED_CA_CERT=$(our_ca_cert) FED_CA_KEY=$CA_KEY
export FED_ROUTER_DOCKER_HOST=ssh://$CORE
cd "$TREE"
out=$(goatbuilder-legacy federate "$THEM" -C . --offline --as "$US" --to 1 --yes 2>&1) || {
	echo "$out"
	first=$(echo "$out" | sed -n 's/^\[✗\] *//p' | head -1)
	die "Preflight refused: ${first:-see the log}"
}
echo "$out"
echo "$out" | grep -q READY || die "Preflight didn't print READY."

show interface "$IFACE (edge port $LISTEN_PORT)"
show endpoint "$FED_PUBLIC_NAME:$PUBLIC_PORT ($([ "$FED_WE_LISTEN" = true ] && echo 'we listen' || echo 'they listen'))"
show link "$SUBNET, MTU 1300"
