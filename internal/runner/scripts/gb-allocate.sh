#!/usr/bin/env bash
# Reserve the link: declare the peering, write the half goatbuilder doesn't
# render, let goatbuilder allocate its values beside the other peerings, and
# commit the lot. Safe to run again: a peering already recorded keeps its values.
. "$FED_SCRIPTS/lib.sh"
STEPS=5
setup_home
sync_repos
gby=$CFG/ops/goatbuilder/goatbuilder.yaml
[ -f "$CFG/ops/federation/$FED_GB_ENVIRONMENT/federation.toml" ] || die "What we offer isn't declared (ops/federation/$FED_GB_ENVIRONMENT/federation.toml). Run the readiness check."

step "Declaring $THEM in the config"
if grep -qE "^- with: $THEM\s*$" "$gby"; then
	echo "$THEM is already declared: its values are kept"
	declared_listen=$(python3 "$FED_SCRIPTS/fedcfg.py" endpoint --repo "$CFG" --them "$THEM")
	if [ "$FED_WE_LISTEN" = true ] && [ -z "$declared_listen" ] && [ -f "$CFG/ops/federation/$FED_GB_ENVIRONMENT/peerings/$THEM/federation.toml" ]; then
		die "$THEM is declared in the config as \"they listen\", but this peering says we listen. Change who listens at the top of this page to match, or change the config entry."
	fi
	if [ "$FED_WE_LISTEN" != true ] && [ -n "$declared_listen" ]; then
		die "$THEM is declared in the config as \"we listen\" ($declared_listen), but this peering says they listen. Change who listens at the top of this page to match."
	fi
else
	fwd=()
	if [ "$FED_WE_LISTEN" = true ]; then
		[ -n "$FED_PUBLIC_NAME" ] || die "We listen, but the forwarder's public name isn't set (federation_public_name)."
		fwd=(--forwarder "$FED_PUBLIC_NAME")
	fi
	python3 "$FED_SCRIPTS/fedcfg.py" gb-declare --repo "$CFG" --them "$THEM" --gb-environment "$FED_GB_ENVIRONMENT" --gb-site "$FED_GB_SITE" \
		--cert-dir "$GBW/pki" "${fwd[@]}" || die "Couldn't declare the peering in goatbuilder.yaml."
fi
python3 "$FED_SCRIPTS/fedcfg.py" gb-inventory --repo "$CFG" --them "$THEM" --edge-name "$FED_EDGE_NAME" --edge-host "$FED_EDGE_HOST" --edge-user "$FED_EDGE_USER" \
	--core-name "$FED_CORE_NAME" --core-host "$FED_CORE_HOST" --core-user "$FED_CORE_USER" || die "Couldn't add the peering's groups to the inventory."
python3 "$FED_SCRIPTS/fedcfg.py" gb-halves --repo "$CFG" --them "$THEM" --site "$FED_SITE" --core-name "$FED_CORE_NAME" --core-host "$FED_CORE_HOST" --waiver "$FED_FIPS_WAIVER" \
	|| die "Couldn't write the peering's settings."
push_config "Declare the $THEM peering (goatbuilder federate)"
assemble

# FED_ALLOCATE_MOVE: a group in the hand-written inventory counts as running on
# role defaults until it's recorded; this lets its first allocation through.
step "goatbuilder federate allocate $THEM: what it would write"
FED_ALLOCATE_MOVE=$THEM gb_run allocate "$THEM" --dry-run --yes || gb_die "goatbuilder won't allocate this peering. The explanation below says what clashes."
step "goatbuilder federate allocate $THEM"
FED_ALLOCATE_MOVE=$THEM gb_run allocate "$THEM" --yes || gb_die "Allocation failed. The log names what to fix."
vals=$(echo "$GB_OUT" | sed -n "s/^ok *values *[^:]*: //p" | head -1 | sed 's/ certs .*//')
step "Committing the link's values"
tree_to_config
push_config "Allocate the $THEM peering (goatbuilder federate allocate)"
ep=$(python3 "$FED_SCRIPTS/fedcfg.py" endpoint --repo "$CFG" --them "$THEM")
if [ -n "$ep" ]; then
	show endpoint "$ep"
	summary "they dial $ep · ${vals:-values committed}"
	echo "::note Tell ${FED_NAME:-$THEM}: dial $ep."
else
	summary "we dial them · ${vals:-values committed}"
fi
[ -n "$vals" ] && show link "$vals"
exit 0
