#!/usr/bin/env bash
# Phases 10-13: carry out the approved plan.
. "$FED_SCRIPTS/lib.sh"
STEPS=3
: "${FED_PLAN_HASH:?Approve the plan first}" "${FED_ADMISSION_DIGEST:?Approve the admission first}"
setup_home
use_plan_tree
step "goatbuilder federate deploy $THEM: phases 10 to 13"
rc=0; gb_run deploy "$THEM" --yes --approve-plan "$FED_PLAN_HASH" --approve-admission "$FED_ADMISSION_DIGEST" || rc=$?
if [ $rc -ne 0 ]; then
	echo "$GB_OUT" | grep -qE "GB-18[678]|GB-190|GB-20[012]" && gb_die "Something the plan was made from has changed since you approved it. Run \"Review the plan\" again and approve the new one."
	gb_die "Deploy stopped (exit $rc). Nothing after the failed phase ran; the log names it. It's safe to try again."
fi
step "Committing what goatbuilder admitted"
git -C "$CFG" fetch -q origin && git -C "$CFG" reset -q --hard origin/main
tree_to_config
push_config "Admit $THEM on the core (goatbuilder federate deploy)"
summary "deployed: core admitted, tunnel open, edge and republisher running"
