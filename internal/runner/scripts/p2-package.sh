#!/usr/bin/env bash
# Phases 2-4: the federation interface on the edge, then our signed package.
. "$FED_SCRIPTS/lib.sh"
STEPS=5
setup_home
sync_repos
alloc
federation_toml
assemble

if [ "$FED_WE_LISTEN" = true ] && ! grep -q '^peering_wg_forwarder_carrier_public_key:' "$CFG/ops/ansible/group_vars/${THEM}_edge/vars.yml"; then
	# The forwarder needs the edge's carrier key, and the edge needs the
	# forwarder's: prepare once to make ours, run the forwarder, then again.
	STEPS=10
	step "First pass: make the edge's carrier key"
	play "${THEM}_edge" peering-prepare.yml || true
	edge_key=$(ssh_edge "sudo -n sh -c 'wg pubkey < /etc/goat-peering-wg/$CARRIER_IFACE.key'") || die "No carrier key on the edge for $CARRIER_IFACE."
	echo "edge carrier key: $edge_key"
	python3 "$FED_SCRIPTS/fedcfg.py" forwarder-add --repo "$CFG" --them "$THEM" --edge-key "$edge_key" \
		--carrier-iface "$CARRIER_IFACE" --public-port "$PUBLIC_PORT" --listen-port "$LISTEN_PORT" \
		--carrier-port "$CARRIER_PORT" --carrier-net "$CARRIER_NET"
	push_config "Forwarder: carry $THEM on $PUBLIC_PORT/$CARRIER_PORT"
	assemble
	step "The forwarder (AWS, over SSM)"
	if ! (cd "$TREE/ops/ansible" && ../scripts/with-secrets ansible-playbook -i inventory/peering-forwarders.ini peering-forwarder.yml) | tee "$W/forwarder.log"; then
		die "The forwarder play failed."
	fi
	fwd_key=$(python3 "$FED_SCRIPTS/fedcfg.py" forwarder-key --log "$W/forwarder.log" --them "$THEM") || die "Couldn't read the forwarder's carrier key from its output."
	python3 "$FED_SCRIPTS/fedcfg.py" set-edge --repo "$CFG" --them "$THEM" --key peering_wg_forwarder_carrier_public_key --value "\"$fwd_key\""
	push_config "Edge: the forwarder's carrier key for $THEM"
	assemble
fi

play "${THEM}_edge" peering-prepare.yml || die "Preparing the edge failed. If it says 'Address already in use', another link holds the port."
ssh_edge "sudo -n wg show $IFACE | head -5" || die "$IFACE isn't up on the edge."

step "goatbuilder federate init --over-ssh (measure and sign our package)"
keep_old ours.canonical.json
cd "$TREE"
goatbuilder federate init --over-ssh "$THEM" -C "$TREE" --with-transport --with-data \
	--signing-key "$SEED" --out "$W/ours.canonical.json" || die "init couldn't measure or sign our package."
step "goat federate lint"
goat federate lint "$W/ours.canonical.json" || die "Lint refused our package."
cp -f "$(our_ca_cert)" "$W/ours-ca.pem"
show package "$(sha256sum "$W/ours.canonical.json" | cut -c1-16)… signed and linted"
