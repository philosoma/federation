#!/usr/bin/env bash
# Allocate (goatbuilder): declare the peering, let goatbuilder choose its values
# and render its files, and commit them to the config for review.
. "$FED_SCRIPTS/lib.sh"
STEPS=5
setup_home
sync_repos
step "Declaring $THEM in goatbuilder.yaml"
decl="ops/federation/$FED_GB_ENVIRONMENT/federation.toml"
[ -f "$CFG/$decl" ] || die "No $decl in the config: what we offer is declared there (sovereign_id, term_days, cut_contact, [data] release). Add it first."
fwd=()
[ "$FED_WE_LISTEN" = true ] && fwd=(--forwarder "$FED_PUBLIC_NAME")
python3 "$FED_SCRIPTS/fedcfg.py" gb-declare --repo "$CFG" --them "$THEM" --gb-environment "$FED_GB_ENVIRONMENT" --gb-site "$FED_GB_SITE" "${fwd[@]}" || die "Couldn't declare the peering."
push_config "Declare the $THEM peering (goatbuilder federate)"
assemble
step "goatbuilder federate allocate $THEM --dry-run"
gb allocate "$THEM" --dry-run --yes || die "allocate refused. Open the log for the GB code (goatbuilder explain GB-nnn)."
step "goatbuilder federate allocate $THEM"
gb allocate "$THEM" --yes || die "allocate failed."
step "Committing the allocated values and files"
tree_to_config
push_config "Allocate the $THEM peering (goatbuilder federate allocate)"
python3 - "$CFG/ops/goatbuilder/peering-values.yaml" "$THEM" <<'PY' || true
import sys, yaml
d = yaml.safe_load(open(sys.argv[1])) or {}
for k, v in (d.get("peerings", d) or {}).items() if isinstance(d, dict) else []:
    if k == sys.argv[2] and isinstance(v, dict):
        print("::show values=" + ", ".join(f"{a} {b}" for a, b in v.items()))
PY
