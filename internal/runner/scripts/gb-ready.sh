#!/usr/bin/env bash
# Readiness: everything the run depends on, checked before anything changes.
# Each line is one condition from "Before you start" in the operator guide.
. "$FED_SCRIPTS/lib.sh"
STEPS=4
setup_home

step "Our tools and keys"
v=$(goatbuilder --version 2>/dev/null) && check ok "goatbuilder" "$v" || check fail "goatbuilder" "not installed in this image"
[ -r "$SEED" ] && check ok "Signing key" "present" || check fail "Signing key" "missing: Federation's deploy installs it"
[ -r "$CA_KEY" ] && check ok "Our CA key" "present (mints the peering certificates)" || check fail "Our CA key" "missing: Federation's deploy installs it"

step "The config"
if { if [ -d "$CFG/.git" ]; then git -C "$CFG" fetch -q origin; else mkdir -p "$REPOS" && git clone -q "$FED_CONFIG_REPO" "$CFG"; fi; } 2>/dev/null; then
	git -C "$CFG" checkout -q -B main origin/main && git -C "$CFG" reset -q --hard origin/main && git -C "$CFG" clean -qfdx
	check ok "Config repo" "main at $(git -C "$CFG" rev-parse --short HEAD)"
	if git -C "$CFG" push -q --dry-run origin HEAD:main 2>/dev/null; then check ok "Can commit to the config" "deploy key has write access"
	else check fail "Can commit to the config" "the deploy key can't push: add it to the config repo with write access"; fi
	decl=ops/federation/$FED_GB_ENVIRONMENT/federation.toml
	if [ -f "$CFG/$decl" ]; then
		rel=$(sed -n 's/^release *= *//p' "$CFG/$decl" | head -1)
		check ok "What we offer" "${rel:-declared} ($decl)"
	else check fail "What we offer" "no $decl: declare sovereign_id, term_days, cut_contact and [data] release there"; fi
	gby=$CFG/ops/goatbuilder/goatbuilder.yaml
	grep -qE '^\s+sites:' "$gby" 2>/dev/null && check ok "goatbuilder.yaml" "sites declared" || check fail "goatbuilder.yaml" "no sites: block (goatcore #404 drops it): restore it"
	grep -q 'peering_directory:' "$gby" 2>/dev/null && check ok "Directory declared" "$(sed -n 's/^ *url: *//p' "$gby" | head -1)" || check warn "Directory declared" "not in goatbuilder.yaml: using $FED_DIRECTORY_URL"
else
	check fail "Config repo" "can't fetch $FED_CONFIG_REPO"
fi

step "The directory and their side"
if curl -fsS -m 15 "$FED_DIRECTORY_URL/healthz" >/dev/null 2>&1; then
	check ok "Directory" "$FED_DIRECTORY_URL answers"
	if curl -fsS -m 15 "$FED_DIRECTORY_URL/v1/parties/$THEM" >/dev/null 2>&1; then check ok "${FED_NAME:-$THEM} on the directory" "they've published a package"
	else check warn "${FED_NAME:-$THEM} on the directory" "nothing published as $THEM yet: the exchange step waits until they run theirs"; fi
else
	check fail "Directory" "$FED_DIRECTORY_URL doesn't answer"
fi
[ -z "${FED_OTHERS_EXCHANGING:-}" ] && check ok "One exchange at a time" "no other exchange is open" \
	|| check warn "One exchange at a time" "open with: $FED_OTHERS_EXCHANGING. The directory holds one package of ours: finish that agreement first, or its package is replaced"

step "Our hosts"
if o=$(ssh_edge "sudo -n true && sudo -n docker ps -q >/dev/null && echo ok" 2>&1) && [ "$o" = ok ]; then check ok "Edge host" "$FED_EDGE_NAME: SSH, sudo and docker work"
else check fail "Edge host" "$FED_EDGE_NAME ($FED_EDGE_HOST): ${o:-unreachable}"; fi
if o=$(ssh_core "sudo -n docker inspect -f '{{.State.Health.Status}}' $FED_SITE-zenoh-router" 2>&1) && [ "$o" = healthy ]; then check ok "Core router" "$FED_CORE_NAME: healthy"
else check fail "Core router" "$FED_CORE_NAME: ${o:-unreachable}"; fi
if [ "$FED_WE_LISTEN" = true ]; then
	if [ -n "$FED_PUBLIC_NAME" ] && getent hosts "$FED_PUBLIC_NAME" >/dev/null 2>&1; then check ok "They dial" "$FED_PUBLIC_NAME ($(getent hosts "$FED_PUBLIC_NAME" | awk '{print $1; exit}'))"
	else check fail "They dial" "we listen, but the forwarder's public name isn't set or doesn't resolve (federation_public_name)"; fi
	if python3 -c 'import boto3; boto3.client("sts").get_caller_identity()' >/dev/null 2>&1; then check ok "Forwarder access" "AWS credentials work"
	else check warn "Forwarder access" "couldn't confirm AWS access for the forwarder from here"; fi
else
	check ok "They listen" "our edge dials them: no forwarder needed"
fi

[ "$CHECK_FAILED" -eq 0 ] || die "$CHECK_FAILED check(s) failed. Fix them, then check again."
summary "all $CHECK_N checks passed"
