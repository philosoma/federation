#!/usr/bin/env bash
# Phases 10-13 (goatbuilder): carry out the approved plan.
. "$FED_SCRIPTS/lib.sh"
STEPS=3
: "${FED_PLAN_HASH:?Approve the plan first}" "${FED_ADMISSION_DIGEST:?Approve the admission first}"
setup_home
use_plan_tree
step "goatbuilder federate deploy $THEM (phases 10 to 13)"
gb deploy "$THEM" --yes --approve-plan "$FED_PLAN_HASH" --approve-admission "$FED_ADMISSION_DIGEST" \
	|| die "Deploy stopped. If an input moved since the plan, make the plan again. The log names the phase."
step "Committing what goatbuilder admitted"
mkdir -p "$REPOS"
git -C "$CFG" fetch -q origin && git -C "$CFG" reset -q --hard origin/main
tree_to_config
push_config "Admit $THEM on the core (goatbuilder federate deploy)"
show deployed "plan ${FED_PLAN_HASH:0:19}…"
