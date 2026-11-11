#!/usr/bin/env bash
# Phase 10: admit the edge on our core. Config to main, then redeploy the core router.
. "$FED_SCRIPTS/lib.sh"
STEPS=6
setup_home
sync_repos
alloc
[ -s "$W/applied/ops/federation/partners/$THEM.yaml" ] || [ -d "$W/applied" ] || die "Run phases 6-9 first."

release=$(grep -rhoE 'release/REL-[A-Z0-9-]+/\*\*' "$W/applied" 2>/dev/null | sort -u | paste -sd, -)
[ -n "$release" ] || release="release/REL-ALL/**"
step "Declaring $THEM on our core: releases $release"
python3 "$FED_SCRIPTS/fedcfg.py" admit --repo "$CFG" --them "$THEM" --us "$US" --site "$FED_SITE" --core-host "$FED_CORE_HOST" \
	--applied "$W/applied" --release "$release" --digest "$(cat "$W/digest.txt" 2>/dev/null)" \
	--expires "$(python3 -c 'import json; a=json.load(open("'"$W"'/agreement.json")); print(a.get("expires",""))' 2>/dev/null)"
git -C "$CFG" --no-pager diff --stat
push_config "Admit $THEM on the core and configure its edge router"
assemble

stamp=$(date -u +%Y%m%d%H%M%S)
step "Backing up the core router's config"
ssh_core "sudo -n cp -a /etc/$FED_SITE-zenoh/config.json5 /etc/$FED_SITE-zenoh/config.json5.pre-$THEM-$stamp && sudo -n cp -a /etc/$FED_SITE-zenoh/tls/ca-root-cert.pem /etc/$FED_SITE-zenoh/tls/ca-root-cert.pem.pre-$THEM-$stamp && echo backed up as *.pre-$THEM-$stamp" \
	|| die "Couldn't back up the core router's config."

step "Deploying the core router (it restarts)"
(cd "$TREE" && ops/scripts/with-secrets bash -c "cd ops/ansible && ansible-playbook -i inventory/mgmt-hosts.ini zenoh-deploy.yml -e target=$FED_CORE_NAME --limit $FED_CORE_NAME") \
	|| die "The core deploy failed. The previous config is on the core as *.pre-$THEM-$stamp."

step "Checking the router"
for i in $(seq 1 24); do
	h=$(ssh_core "sudo -n docker inspect -f '{{.State.Health.Status}}' $FED_SITE-zenoh-router" 2>/dev/null || true)
	[ "$h" = healthy ] && break
	sleep 5
done
echo "router: ${h:-unknown}"
[ "$h" = healthy ] || die "The core router isn't healthy after the deploy."
ssh_core "sudo -n grep -c 'fed.$THEM.$US.01' /etc/$FED_SITE-zenoh/config.json5" >/dev/null || die "The core's config doesn't name fed.$THEM.$US.01."
show admitted "fed.$THEM.$US.01 and fed.$US.$THEM.republisher.01"
