#!/usr/bin/env python3
"""The few edits Federation makes to the config overlay itself. goatbuilder
federate writes everything else (allocate renders the peering's files, its
admission writes the partner's name and CN on the core).

  gb-declare  the peering's entry in ops/goatbuilder/goatbuilder.yaml
  gb-reads    what the partner may read on our core (its own prefix)

Line edits, not a YAML round trip: the files carry comments people rely on.
"""
import argparse
import os
import re
import sys

# The acceptance check and the round-trip responder stage under the set itself,
# shared by every partner: test data only.
ACCEPTANCE_KEYS = ("acceptance", "fedcheck-ack")


def read(p):
    with open(p, encoding="utf-8") as f:
        return f.read()


def write(p, s):
    os.makedirs(os.path.dirname(p), exist_ok=True)
    with open(p, "w", encoding="utf-8") as f:
        f.write(s)


def q(s):
    return '"' + s + '"'


def partner_reads(us, them, sets):
    """What a partner may read on our core: its own prefix in each set
    (from/<us>/<set>/<them>/**, where the release bridge stages what we send
    it), plus the acceptance keys. Never the whole set: every partner's copy
    lives under it."""
    out = []
    for s in sets:
        base = f"from/{us}/{s}"
        out.append(f"{base}/{them}/**")
        out += [f"{base}/{k}/**" for k in ACCEPTANCE_KEYS]
    return out


def peerings_file(repo, site):
    """Where the core group's zenoh_sovereign_peerings lives. goatbuilder's
    admission writes, and requires, <core group>/peerings.yml (GB-237)."""
    d = os.path.join(repo, "ops/ansible/group_vars", f"{site.replace('-', '_')}_hosts")
    new, old = os.path.join(d, "peerings.yml"), os.path.join(d, "sovereign-peerings.yml")
    return old if os.path.exists(old) and not os.path.exists(new) else new


def gb_declare(a):
    """The peering's entry in goatbuilder.yaml. Edits only the peerings: block
    (never sites:, which goatcore's gen-config drops, goatcore #404)."""
    path = os.path.join(a.repo, "ops/goatbuilder/goatbuilder.yaml")
    gb = read(path)
    if not re.search(r"^\s+sites:", gb, re.M):
        sys.exit("goatbuilder.yaml has no sites: block; restore it before declaring a peering (goatcore #404)")
    if re.search(rf"^- with: {re.escape(a.them)}\s*$", gb, re.M):
        print(f"{a.them} is already declared")
        return
    entry = f"- with: {a.them}\n  environment: {a.gb_environment}\n  site: {a.gb_site}\n  edge_class: edge\n  tunnel: wireguard\n"
    if a.forwarder:
        entry += f"  forwarder:\n    endpoint: {a.forwarder}\n"
        if a.forwarder_group:
            entry += f"    group: {a.forwarder_group}\n"
    if re.search(r"^peerings:\s*$", gb, re.M):
        gb = gb.rstrip("\n") + "\n" + entry
    else:
        gb = gb.rstrip("\n") + "\npeerings:\n" + entry
    write(path, gb)
    print(f"declared {a.them}")


def gb_reads(a):
    """A partner's reads on our core. goatbuilder's admission writes only
    sovereign and cn and keeps the other keys, so the partner's own prefix is
    ours to write: without release_key_exprs the role renders "read NOTHING"."""
    us, them = a.us, a.them
    sp_p = peerings_file(a.repo, a.site)
    sp = read(sp_p) if os.path.exists(sp_p) else "zenoh_sovereign_peerings:\n"
    if re.search(rf"^\s*- sovereign: {re.escape(them)}\s*$", sp, re.M):
        print(f"{them} already has an entry in {os.path.basename(sp_p)}")
        return
    sets = [s for s in a.sets.split(",") if s]
    reads = partner_reads(us, them, sets)
    sp = sp.rstrip("\n") + (
        f"\n  - sovereign: {them}\n"
        f"    cn: fed.{them}.{us}.01\n"
        f"    release_key_exprs: [{', '.join(q(r) for r in reads)}]  # its own prefix + acceptance keys\n"
        f"    release_interest_key_exprs: [\"from/{us}/**\"]\n"
        f"    republisher_cn: fed.{us}.{them}.republisher.01\n"
        f"    republisher_release_key_exprs: [{', '.join(q(f'release/{s}/**') for s in sets)}]\n"
    )
    write(sp_p, sp)
    print(f"{them}: reads {', '.join(reads)} ({os.path.basename(sp_p)})")


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("gb-declare")
    for k in ("repo", "them", "gb-environment", "gb-site"):
        p.add_argument("--" + k, required=True)
    p.add_argument("--forwarder", default="")
    p.add_argument("--forwarder-group", default="")
    p = sub.add_parser("gb-reads")
    for k in ("repo", "them", "us", "site"):
        p.add_argument("--" + k, required=True)
    p.add_argument("--sets", default="REL-ALL")
    a = ap.parse_args()
    if not re.fullmatch(r"[a-z0-9][a-z0-9-]{1,15}", a.them):
        sys.exit("bad sovereign id")
    {"gb-declare": gb_declare, "gb-reads": gb_reads}[a.cmd](a)


if __name__ == "__main__":
    main()
