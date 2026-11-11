#!/usr/bin/env bash
# Phases 6-9 with the confirmed digest, then the bounded plan and what our
# core will admit. Changes no host.
. "$FED_SCRIPTS/lib.sh"
STEPS=5
: "${FED_THEIR_KEY:?}" "${FED_THEIR_CA:?}" "${FED_DIGEST:?Confirm the digest first}"
setup_home
sync_repos
step "What $THEM may read on our core: its own prefix and the acceptance keys"
python3 "$FED_SCRIPTS/fedcfg.py" gb-reads --repo "$CFG" --them "$THEM" --us "$US" --site "$FED_SITE" || die "Couldn't write $THEM's reads to the config."
push_config "What $THEM reads on our core: its own prefix (Federation)"
assemble
step "goatbuilder federate $THEM: phases 6 to 9"
gb_run "$THEM" --yes --partner-key "$FED_THEIR_KEY" --partner-ca-fingerprint "$FED_THEIR_CA" --agreement-digest "$FED_DIGEST" \
	|| gb_die "Phases 6 to 9 stopped. The log names the phase and what to fix."
step "goatbuilder federate plan $THEM"
gb_run plan "$THEM" || gb_die "The plan is blocked: its conflicts and readiness rows are in the log."
h=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["hash"])' "$GBW/plan.json" 2>/dev/null) || h=
[ -n "$h" ] || gb_die "goatbuilder wrote no plan hash."
step "What our core will admit"
rm -rf "$GBW/admission/review.md"
rc=0; gb_run deploy "$THEM" --yes --approve-plan "$h" --review-in-pr || rc=$?
a=$(echo "$GB_OUT" | grep -oE "approve-admission sha256:[0-9a-f]{64}" | head -1 | awk '{print $2}')
[ -n "$a" ] || a=$(grep -ohE "sha256:[0-9a-f]{64}" "$GBW/admission/review.md" 2>/dev/null | tail -1) || true
[ -n "$a" ] || gb_die "goatbuilder didn't say what our core will admit (exit $rc). Open the log."
show raw_plan_hash "$h"
show raw_admission_digest "$a"
summary "plan ${h:0:19}… approved"
echo "::note Read the plan and the admission, then approve both."
