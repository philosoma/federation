#!/usr/bin/env bash
# Link status, read-only: what the page's Status view shows. Runs every
# minute beside the phases, so it uses its own config clone and never
# changes anything. Prints one JSON object.
set -uo pipefail
umask 077
: "${FED_STATE:?}" "${FED_US:?}"
: "${FED_SECRETS:=/run/secrets/federation}"
: "${FED_SITE:?}" "${FED_CONFIG_REPO:?}" "${FED_EDGE_HOST:?}" "${FED_EDGE_USER:?}" "${FED_CORE_HOST:?}" "${FED_CORE_USER:?}"
export HOME=$FED_STATE/home GIT_TERMINAL_PROMPT=0
CFG=$FED_STATE/repos/config-status
mkdir -p "$HOME/.ssh" "$FED_STATE/repos"
# The phases write the full SSH config; this is enough on its own.
[ -f "$HOME/.ssh/config" ] || cat >"$HOME/.ssh/config" <<EOF
Host github.com
  IdentityFile $FED_SECRETS/github-key
  IdentitiesOnly yes
Host $FED_EDGE_HOST $FED_CORE_HOST
  IdentityFile $FED_SECRETS/ssh-key
  IdentitiesOnly yes
Host *
  UserKnownHostsFile $FED_STATE/known_hosts
  StrictHostKeyChecking accept-new
  BatchMode yes
  ConnectTimeout 10
EOF

errors=()
if [ -d "$CFG/.git" ]; then
	git -C "$CFG" fetch -q origin 2>/dev/null && git -C "$CFG" reset -q --hard origin/main 2>/dev/null || errors+=("config: could not fetch")
else
	git clone -q --depth 1 "$FED_CONFIG_REPO" "$CFG" 2>/dev/null || errors+=("config: could not clone")
fi

# Admitted partners and their interface, from the config.
links=$(python3 - "$CFG" "$FED_SITE" <<'PY'
import os, re, sys
cfg, site = sys.argv[1], sys.argv[2]
gv = os.path.join(cfg, "ops/ansible/group_vars", f"{site.replace('-', '_')}_hosts")
sp = os.path.join(gv, "peerings.yml")  # goatbuilder 0.5's file; sovereign-peerings.yml before it
if not os.path.exists(sp):
    sp = os.path.join(gv, "sovereign-peerings.yml")
text = open(sp).read() if os.path.exists(sp) else ""
ends = {m.group(1).lower(): (m.group(2), m.group(3)) for m in re.finditer(r"^#\s+([A-Z0-9][A-Z0-9-]{1,15})\s+(sha256:[0-9a-f]+)[^\n]*?through (\d{4}-\d{2}-\d{2})", text, re.M)}
for them in re.findall(r"^\s*- sovereign:\s*([a-z0-9-]+)", text, re.M):
    ev = os.path.join(cfg, "ops/ansible/group_vars", f"{them}_edge", "vars.yml")
    m = re.search(r"^peering_wg_interface:\s*(\S+)", open(ev).read(), re.M) if os.path.exists(ev) else None
    digest, end = ends.get(them, ("", ""))
    print(them, m.group(1) if m else "wg-fed0", digest or "-", end or "-")
PY
)

ifaces=$(echo "$links" | awk 'NF{print $2}' | sort -u | tr '\n' ' ')
edge=$(ssh "$FED_EDGE_USER@$FED_EDGE_HOST" "now=\$(date +%s); echo now \$now; for i in $ifaces; do echo hs \$i \$(sudo -n wg show \$i latest-handshakes 2>/dev/null | awk '{print \$2}' | sort -n | tail -1); echo tr \$i \$(sudo -n wg show \$i transfer 2>/dev/null | awk '{r+=\$2; t+=\$3} END{print r+0, t+0}'); done; sudo -n docker ps --format 'ctr {{.Names}} {{.Status}}' | grep goat-zenoh-edge-; true" 2>/dev/null) || errors+=("edge: unreachable")
core=$(ssh "$FED_CORE_USER@$FED_CORE_HOST" "sudo -n docker ps --format 'ctr {{.Names}} {{.Status}}' | grep goat-fed-republisher-; true" 2>/dev/null) || errors+=("core: unreachable")

# What goatbuilder federate recorded for the peerings it runs (read-only).
if command -v goatbuilder >/dev/null && [ -d "$FED_STATE/gb" ]; then
	FED_WORK_ROOT=$FED_STATE/gb goatbuilder federate status -o json -C "$CFG" --color never >"$FED_STATE/gb-status.json.tmp" 2>/dev/null \
		&& mv "$FED_STATE/gb-status.json.tmp" "$FED_STATE/gb-status.json"
	rm -f "$FED_STATE/gb-status.json.tmp"
fi

python3 - "$links" "$edge" "$core" "${errors[@]+"${errors[@]}"}" <<'PY'
import json, sys, time
links_s, edge_s, core_s, errors = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4:]
now, hs, tr, ctr = None, {}, {}, {}
for line in (edge_s + "\n" + core_s).splitlines():
    p = line.split()
    if not p:
        continue
    if p[0] == "now":
        now = int(p[1])
    elif p[0] == "hs" and len(p) == 3:
        hs[p[1]] = int(p[2])
    elif p[0] == "tr" and len(p) == 4:
        tr[p[1]] = (int(p[2]), int(p[3]))
    elif p[0] == "ctr" and len(p) >= 3:
        ctr[p[1]] = " ".join(p[2:])
out = []
for line in links_s.splitlines():
    p = line.split()
    if len(p) != 4:
        continue
    them, iface, digest, end = p
    h = hs.get(iface, 0)
    out.append({
        "them": them, "iface": iface,
        "digest": "" if digest == "-" else digest,
        "expires": "" if end == "-" else end,
        "handshake_age": (now - h) if (now and h) else None,
        "rx": tr.get(iface, (0, 0))[0], "tx": tr.get(iface, (0, 0))[1],
        "edge_router": ctr.get(f"goat-zenoh-edge-{them}", ""),
        "republisher": ctr.get(f"goat-fed-republisher-{them}", ""),
    })
print(json.dumps({"checked": int(time.time()), "links": out, "errors": errors}))
PY
