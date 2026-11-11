#!/usr/bin/env bash
# What we send a partner: set its topics in the release file, commit it, and
# restart the release bridge on the core with it. FED_SEND is the topic ids to
# send (space-separated, possibly empty). Touches nothing else: no router
# restart, no other partner.
. "$FED_SCRIPTS/lib.sh"
: "${FED_SEND?}"
: "${FED_RELEASE_FILE:=ops/release-bridge/release-bridge.json}"
STEPS=3
setup_home

step "Updating the release file"
mkdir -p "$REPOS"
if [ -d "$CFG/.git" ]; then git -C "$CFG" fetch -q origin; else git clone -q "$FED_CONFIG_REPO" "$CFG"; fi
git -C "$CFG" checkout -q -B main origin/main
git -C "$CFG" reset -q --hard origin/main
git -C "$CFG" clean -qfdx
RF=$CFG/$FED_RELEASE_FILE
[ -f "$RF" ] || die "No release file at $FED_RELEASE_FILE in the config. See the README: Choosing what each partner gets."
summary=$(python3 - "$RF" "$THEM" "$FED_SEND" 2>&1 <<'PY'
import json, sys
path, them, want = sys.argv[1], sys.argv[2], sys.argv[3].split()
cfg = json.load(open(path))
topics = {t["id"]: t for t in cfg.get("topics", [])}
bad = [t for t in want if t not in topics]
if bad:
    sys.exit("unknown topic: " + ", ".join(bad))
if cfg.get("map"):
    sys.exit("the release file still has the old shared 'map' list: move it to topics and send first")
send = cfg.setdefault("send", {})
before = send.get(them, [])
send[them] = [t["id"] for t in cfg["topics"] if t["id"] in want]   # catalogue order
cfg["send"] = dict(sorted(send.items()))
with open(path, "w") as f:
    json.dump(cfg, f, indent=2)
    f.write("\n")
names = lambda ids: ", ".join(topics[i].get("name", i) for i in ids) or "nothing"
print(f"{names(before)} -> {names(send[them])}")
PY
) || die "Couldn't update the release file: $summary"
echo "$THEM: $summary"
push_config "Release: send ${FED_NAME:-$THEM} ${summary#* -> }"

step "Installing the release bridge on the core"
scp -q "$FED_SCRIPTS/release-bridge.py" "$FED_SCRIPTS/release-bridge.service" "$RF" "$CORE:/tmp/" || die "Couldn't copy the release bridge to the core."
ssh_core "sudo -n sh -c 'set -e; install -d -m0755 /opt/release-bridge /etc/release-bridge; install -m0755 /tmp/release-bridge.py /opt/release-bridge/release-bridge.py; install -m0644 /tmp/$(basename "$RF") /etc/release-bridge/release-bridge.json; if ! cmp -s /tmp/release-bridge.service /etc/systemd/system/release-bridge.service; then install -m0644 /tmp/release-bridge.service /etc/systemd/system/; systemctl daemon-reload; systemctl enable -q release-bridge; fi; rm -f /tmp/release-bridge.py /tmp/release-bridge.service /tmp/$(basename "$RF")'" \
	|| die "Couldn't install the release bridge on the core."

step "Restarting the release bridge"
ssh_core "sudo -n systemctl restart release-bridge && sleep 4 && systemctl is-active release-bridge && sudo -n journalctl -u release-bridge --since '-10s' --no-pager -o cat | tail -20" \
	|| die "The release bridge didn't start. Open the log."
show sending "${summary#* -> }"
