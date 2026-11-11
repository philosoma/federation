#!/usr/bin/env bash
# Phase 12: the edge router, then the republisher on our core.
. "$FED_SCRIPTS/lib.sh"
STEPS=6
setup_home
sync_repos
alloc
assemble
[ -s "$W/pki/peering-outward.pem" ] || die "No peering certificates. Run phases 6-9."

step "Placing the certificates on the edge"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp "$W"/pki/peering-*.pem "$tmp/"
cp "$W/theirs-ca.pem" "$tmp/partner-ca.pem"
cp "$W/ours-ca.pem" "$tmp/our-ca.pem"
d=/etc/goat-zenoh-edge-$THEM/tls
# Only the .pem files go in the archive: packing "." would carry the temporary
# folder's 0700 mode onto the edge's tls folder. Every step runs as root on the
# edge, because the folder is root-only and the login user can't expand *.pem in it.
(cd "$tmp" && tar -cf - ./*.pem) | ssh_edge "sudo -n sh -c 'set -e; install -d -m0750 -o root -g root /etc/goat-zenoh-edge-$THEM $d; tar -C $d --no-same-owner -xf -; chown root:root $d/*.pem; chmod 0644 $d/*.pem; chmod 0600 $d/*-key.pem; chmod 0750 $d; ls -l $d'" \
	|| die "Couldn't copy the certificates to the edge."

play "${THEM}_edge" peering-zenoh-edge.yml || die "Deploying the edge router failed."
play "${THEM}_core" peering-republisher.yml || die "Deploying the republisher failed."

step "Checking the edge router admits our core"
sleep 10
if ssh_edge "sudo -n docker logs goat-zenoh-edge-$THEM 2>&1 | grep -i 'did not match any configured ACL subject'"; then
	die "The edge router denies a link: some subject has no ACL. Check goat_zenoh_edge_core_cn."
fi
ssh_edge "sudo -n docker ps --filter name=goat-zenoh-edge-$THEM --format '{{.Names}} {{.Status}}'"
ssh_core "sudo -n docker ps --filter name=goat-fed-republisher-$THEM --format '{{.Names}} {{.Status}}'"

# Our checks pass without the partner, so wait for their edge router too: a
# session between the two link addresses on the edge port, whichever side dialled.
tf=$W/applied/ops/ansible/group_vars/${THEM}_edge/federation-$THEM-transport.yml
local_ip=$(sed -n 's/^goat_peering_local_link_ip: *"\{0,1\}\([0-9.]*\).*/\1/p' "$tf" 2>/dev/null)
remote_ip=$(sed -n 's/^goat_peering_remote_link_ip: *"\{0,1\}\([0-9.]*\).*/\1/p' "$tf" 2>/dev/null)
[ -n "$local_ip" ] && [ -n "$remote_ip" ] || die "Can't read our link addresses from $tf. Run phases 6-9 again."
show edge "goat-zenoh-edge-$THEM on $local_ip:$EDGE_PORT; republisher running"
step "Waiting for ${FED_NAME:-$THEM}'s edge router to connect"
for i in $(seq 1 18); do
	if ssh_edge "ss -tn state established | grep '$local_ip:' | grep '$remote_ip:' | grep -q -E ':$EDGE_PORT( |\$)'"; then
		show partner "${FED_NAME:-$THEM}'s edge ($remote_ip) is connected"
		exit 0
	fi
	sleep 5
done
waiting "Our side is up. ${FED_NAME:-$THEM}'s edge router hasn't connected to $local_ip:$EDGE_PORT yet: once their phase 12 is done, run ours again."
