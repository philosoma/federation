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
    # deployment: hand, because the rendered half turns SPIRE federation on;
    # gb-halves writes the hand half. federation.toml is rendered from what we
    # offer and the allocated values. cert_dir is Federation's work directory.
    entry = (f"- with: {a.them}\n  environment: {a.gb_environment}\n  site: {a.gb_site}\n  edge_class: edge\n  tunnel: wireguard\n"
             "  deployment: hand\n  federation_toml: rendered\n")
    if a.forwarder:
        entry += f"  forwarder: {{endpoint: {a.forwarder}}}\n"
    entry += f"  values: {{cert_dir: {a.cert_dir}}}\n"
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


def gb_inventory(a):
    """The peering's two groups in the hand inventory goatbuilder reads. Each
    peering gets its own inventory names for the same two machines: a host that
    sat in two peerings' groups would take both peerings' variables."""
    path = os.path.join(a.repo, "ops/ansible/inventory/peering-edges.ini")
    inv = read(path) if os.path.exists(path) else ""
    them = a.them
    if re.search(rf"^\[{re.escape(them)}_edge\]", inv, re.M):
        print(f"{them}'s groups are already in the inventory")
        return
    block = (
        f"# {them.upper()} peering (Federation).\n"
        f"[{them}_edge]\n{a.edge_name}-{them} ansible_host={a.edge_host} ansible_user={a.edge_user} ansible_python_interpreter=/usr/bin/python3\n\n"
        f"[{them}_core]\n{a.core_name}-{them} ansible_host={a.core_host} ansible_user={a.core_user} ansible_python_interpreter=/usr/bin/python3\n\n"
    )
    for group, member in (("peering_edges", f"{them}_edge"), ("peering_cores", f"{them}_core")):
        head = f"[{group}:children]"
        if head in inv:
            inv = inv.replace(head, head + "\n" + member, 1)
        else:
            inv = inv.rstrip("\n") + f"\n\n{head}\n{member}\n"
    # Before the first :children section, so host groups stay together.
    m = re.search(r"^\[peering_edges:children\]", inv, re.M)
    inv = inv[: m.start()] + block + inv[m.start():]
    write(path, inv)
    print(f"added {them}_edge and {them}_core to the inventory")


def site_value(repo, site, key):
    """A site value as it is written in the config: from the core group's
    federation-site.yml, else its generated pins.yml (images are governed
    pins), else another peering's half (the edge router's image lives there)."""
    import glob
    gv = os.path.join(repo, "ops/ansible/group_vars")
    core = os.path.join(gv, f"{site.replace('-', '_')}_hosts")
    files = [os.path.join(core, "federation-site.yml"), os.path.join(core, "pins.yml")]
    files += sorted(glob.glob(os.path.join(gv, "*_edge", "vars.yml")) + glob.glob(os.path.join(gv, "*_core", "vars.yml")))
    for f in files:
        m = re.search(rf"^{key}:\s*(.+)$", read(f), re.M) if os.path.exists(f) else None
        if m:
            return m.group(1).strip()
    sys.exit(f"{key} isn't set anywhere in the config: add it to group_vars/{site}_hosts/federation-site.yml (see the README)")


def gb_halves(a):
    """The hand half of each side: what goatbuilder's allocate doesn't render.
    Idempotent: only keys that are missing are added, so a half written before
    is completed, never rewritten."""
    them, d = a.them, f"/etc/goat-zenoh-edge-{a.them}/tls"
    edge = [
        ("peering_wg_fips_required", "true"),
        ("peering_wg_fips_waived_reason", q(a.waiver)),
        ("peering_spire_enabled", "false   # our SPIRE federation is off (phase 13 reports NOT DONE)"),
        ("peering_spire_server_host", f"{a.core_name}   # unused while SPIRE is off; delegate_to is evaluated before `when`"),
        ("goat_zenoh_edge_image", site_value(a.repo, a.site, "goat_zenoh_edge_image")),
        ("goat_zenoh_edge_trust_mode", "fragment"),
        ("goat_zenoh_edge_partner_ca_path", f"{d}/partner-ca.pem   # {them.upper()}'s CA only, never a core trust file"),
        ("goat_zenoh_edge_our_ca_path", f"{d}/our-ca.pem"),
        ("goat_zenoh_edge_core_endpoints", f"\n  - \"tls/{a.core_host}:7447#root_ca_certificate_file={d}/our-ca.pem\""),
        ("goat_zenoh_edge_core_interface", "wt0"),
        ("goat_zenoh_edge_partner_interface", '"{{ peering_wg_interface }}"'),
        ("goat_zenoh_edge_core_cn", site_value(a.repo, a.site, "goat_zenoh_edge_core_cn")),
        ("goat_zenoh_edge_install_certs", "true"),
    ]
    core = [
        ("goat_fed_republisher_image", site_value(a.repo, a.site, "goat_fed_republisher_image")),
        ("goat_fed_republisher_core_endpoint", f"\"tls/{a.core_host}:7447\""),
        ("goat_fed_republisher_core_ca_src", f"\"{{{{ playbook_dir }}}}/../enrollment/ca/{a.site}/root-cert.pem\""),
    ]
    for half, keys in (("edge", edge), ("core", core)):
        path = os.path.join(a.repo, "ops/ansible/group_vars", f"{them}_{half}", "vars.yml")
        s = read(path) if os.path.exists(path) else (
            f"# {them.upper()} peering, {half} half. Written by Federation. Interface, ports and\n"
            "# certificate paths are in peering-values.yml (goatbuilder federate allocate).\n")
        added = []
        for k, v in keys:
            if not re.search(rf"^{k}:", s, re.M):
                s = s.rstrip("\n") + f"\n{k}: {v}".replace(": \n", ":\n") + "\n"
                added.append(k)
        write(path, s)
        print(f"{them}_{half}: " + (f"added {', '.join(added)}" if added else "complete"))


def endpoint(a):
    """The address the partner dials when we listen, from the rendered
    federation.toml. Prints nothing when we don't listen."""
    import glob
    for f in glob.glob(os.path.join(a.repo, "ops/federation/*/peerings", a.them, "federation.toml")):
        t = read(f)
        m = re.search(r'^endpoint\s*=\s*"([^"]+)"', t, re.M)
        if m and re.search(r"^listener\s*=\s*true", t, re.M):
            print(m.group(1))
        return


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("gb-declare")
    for k in ("repo", "them", "gb-environment", "gb-site"):
        p.add_argument("--" + k, required=True)
    p.add_argument("--forwarder", default="")
    p.add_argument("--cert-dir", required=True)
    p = sub.add_parser("gb-inventory")
    for k in ("repo", "them", "edge-name", "edge-host", "edge-user", "core-name", "core-host", "core-user"):
        p.add_argument("--" + k, required=True)
    p = sub.add_parser("gb-halves")
    for k in ("repo", "them", "site", "core-name", "core-host", "waiver"):
        p.add_argument("--" + k, required=True)
    p = sub.add_parser("endpoint")
    for k in ("repo", "them"):
        p.add_argument("--" + k, required=True)
    p = sub.add_parser("gb-reads")
    for k in ("repo", "them", "us", "site"):
        p.add_argument("--" + k, required=True)
    p.add_argument("--sets", default="REL-ALL")
    a = ap.parse_args()
    if not re.fullmatch(r"[a-z0-9][a-z0-9-]{1,15}", a.them):
        sys.exit("bad sovereign id")
    {"gb-declare": gb_declare, "gb-reads": gb_reads, "gb-inventory": gb_inventory, "gb-halves": gb_halves,
     "endpoint": endpoint}[a.cmd](a)


if __name__ == "__main__":
    main()
