#!/usr/bin/env python3
"""Edits a goatnet config overlay for one peering, in the shape the goatcore
peering roles expect.

Text edits, not a YAML round trip, so the files keep their comments. Every
command is idempotent: run twice, it changes nothing the second time.
"""
import argparse
import glob
import os
import re
import shutil
import sys


def read(p):
    with open(p) as f:
        return f.read()


def write(p, s):
    os.makedirs(os.path.dirname(p), exist_ok=True)
    with open(p, "w") as f:
        f.write(s)


US_LABEL = "US"  # set from --us in main()


def first_match(repo, group_glob, pattern):
    """The first line matching pattern in any group_vars/<group_glob>/vars.yml."""
    for p in sorted(glob.glob(os.path.join(repo, "ops/ansible/group_vars", group_glob, "vars.yml"))):
        m = re.search(pattern, read(p), re.M)
        if m:
            return m.group(0)
    return ""


def edge_vars(repo, them):
    return os.path.join(repo, "ops/ansible/group_vars", f"{them}_edge", "vars.yml")


def index_of(path):
    m = re.search(r"^peering_wg_interface:\s*\"?wg-fed(\d+)", read(path), re.M)
    return int(m.group(1)) if m else 0  # the role default is wg-fed0


def next_index(a):
    own = edge_vars(a.repo, a.them)
    if os.path.exists(own):
        print(index_of(own))
        return
    used = {index_of(p) for p in glob.glob(os.path.join(a.repo, "ops/ansible/group_vars/*_edge/vars.yml"))}
    n = 0
    while n in used:
        n += 1
    print(n)


def set_scalar(text, key, value):
    """Set `key: value` on its own line, replacing it if present."""
    line = f"{key}: {value}"
    pat = re.compile(rf"^{re.escape(key)}:.*$", re.M)
    if pat.search(text):
        return pat.sub(lambda _: line, text, count=1)
    return text.rstrip("\n") + "\n" + line + "\n"


def q(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


MARK = "peering (Federation)."
LEGACY_US = "UK"  # blocks written before the sovereign id was a setting


def hosts_in_other_groups(inv, them):
    """Host names listed under any [<x>_edge] / [<x>_core] group of another peering."""
    names, group = set(), None
    for line in inv.splitlines():
        m = re.match(r"^\[([^\]]+)\]", line)
        if m:
            group = m.group(1)
            continue
        if group and re.fullmatch(r"[a-z0-9-]+_(edge|core)", group) and not group.startswith(them + "_") and line.strip() and not line.startswith("#"):
            names.add(line.split()[0])
    return names


def drop_federation_block(inv, them):
    """Remove the edge/core sections Federation wrote for this peering, if any."""
    i = -1
    for us in (US_LABEL, LEGACY_US):
        i = inv.find(f"# {us} <-> {them.upper()} {MARK}")
        if i >= 0:
            break
    if i < 0:
        return inv
    j = inv.find(f"[{them}_core]", i)
    k = inv.find("\n", inv.find("\n", j) + 1) if j >= 0 else -1  # header line, then its host line
    if j < 0 or k < 0:
        return inv
    return inv[:i].rstrip("\n") + "\n" + inv[k + 1:].lstrip("\n")


def declare(a):
    repo, them = a.repo, a.them
    # Inventory: the edge and core groups, under the children groups. A machine
    # another peering already names gets its own name here (same ansible_host):
    # one name in two peering groups merges both groups' variables (preflight 5c).
    inv_p = os.path.join(repo, "ops/ansible/inventory/peering-edges.ini")
    inv = drop_federation_block(read(inv_p), them)
    if f"[{them}_edge]" not in inv:
        taken = hosts_in_other_groups(inv, them)
        edge_name = a.edge_name if a.edge_name not in taken else f"{a.edge_name}-{them}"
        core_name = a.core_name if a.core_name not in taken else f"{a.core_name}-{them}"
        block = (
            f"\n# {US_LABEL} <-> {them.upper()} {MARK}\n"
            f"[{them}_edge]\n{edge_name} ansible_host={a.edge_host} ansible_user={a.edge_user} ansible_python_interpreter=/usr/bin/python3\n\n"
            f"[{them}_core]\n{core_name} ansible_host={a.core_host} ansible_user={a.core_user} ansible_python_interpreter=/usr/bin/python3\n"
        )
        first_children = inv.find("[peering_edges:children]")
        inv = inv[:first_children].rstrip("\n") + "\n" + block + "\n" + inv[first_children:] if first_children >= 0 else inv + block
    for group, child in (("peering_edges", f"{them}_edge"), ("peering_cores", f"{them}_core")):
        hdr = f"[{group}:children]"
        if hdr not in inv:
            inv = inv.rstrip("\n") + f"\n\n{hdr}\n"
        start = inv.index(hdr) + len(hdr)
        nxt = inv.find("\n[", start)
        section = inv[start: nxt if nxt >= 0 else len(inv)]
        if not re.search(rf"^{re.escape(child)}$", section, re.M):
            inv = inv[:start] + section.rstrip("\n") + f"\n{child}\n" + (inv[nxt:] if nxt >= 0 else "")
    write(inv_p, inv)

    # Edge vars.
    ev = edge_vars(repo, them)
    tail = ""
    if os.path.exists(ev):
        cur = read(ev)
        ours = "Written by Federation." in cur.split("\n", 1)[0]
        cut = cur.find("\n\n# --- Sovereign edge router")
        tail = cur[cut:] if (ours and cut >= 0) else ""
        if not ours:
            ev = None  # hand-written: leave it alone
    if ev:
        s = (
            f"# {US_LABEL} <-> {them.upper()} peering edge ({a.edge_name}). Written by Federation.\n"
            "peering_wg_enabled: true\n\n"
            "# FIPS: the operator's recorded waiver (goat-peering-wg refuses without one).\n"
            "peering_wg_fips_required: true\n"
            f"peering_wg_fips_waived_reason: {q(a.waiver)}\n\n"
            f"peering_wg_interface: {a.iface}\n"
            f"peering_wg_listen_port: {a.listen_port}   # not 51820: NetBird's wt0 holds it on a mesh edge\n"
            f"goat_zenoh_edge_listen_port: {a.edge_port}   # the port partners dial; each peering binds its own link address\n"
        )
        if a.we_listen == "true":
            s += (
                "\n# We listen through the peering forwarder (ADR 1218).\n"
                "peering_wg_via_forwarder: true\n"
                f"peering_wg_forwarder_endpoint: \"{a.public_name}:{a.carrier_port}\"\n"
                f"peering_wg_carrier_interface: {a.carrier_iface}\n"
                f"peering_wg_carrier_listen_port: {a.carrier_local_port}\n"
                f"peering_wg_carrier_local_ip: {a.carrier_net}.1\n"
                f"peering_wg_carrier_remote_ip: {a.carrier_net}.0\n"
            )
        else:
            s += "\n# They listen; our edge dials them.\npeering_wg_via_forwarder: false\n"
        s += (
            "\n# SPIRE federation (phase 13) deferred.\n"
            "peering_spire_enabled: false\n"
            f"peering_spire_server_host: {a.core_name}   # unused while disabled; works around delegate_to evaluating before `when`\n"
        )
        # Settings a later phase added to this section (phase 2-4 adds the
        # forwarder's carrier key) are kept, and so is the phase-10 block.
        if os.path.exists(ev):
            prev = read(ev)
            pcut = prev.find("\n\n# --- Sovereign edge router")
            head = prev[:pcut] if pcut >= 0 else prev
            have = set(re.findall(r"^([a-z_][a-z0-9_]*):", s, re.M))
            kept = [l for l in head.splitlines()
                    if re.match(r"^([a-z_][a-z0-9_]*):", l) and re.match(r"^([a-z_][a-z0-9_]*):", l).group(1) not in have]
            if kept:
                s = s.rstrip("\n") + "\n" + "\n".join(kept) + "\n"
        write(ev, s + ("\n" + tail.lstrip("\n") if tail else ""))

    # Core vars for the republisher. The image pin is taken from an existing
    # peering's, so every republisher on the core runs the same build.
    cv = os.path.join(repo, "ops/ansible/group_vars", f"{them}_core", "vars.yml")
    if not os.path.exists(cv):
        image = first_match(repo, "*_core", r"^goat_fed_republisher_image:.*$")
        write(cv, (
            f"# {US_LABEL} <-> {them.upper()} outbound republisher on the core (peering phase 12). Written by Federation.\n"
            + (image + "\n" if image else "# goat_fed_republisher_image: set it, digest-pinned\n")
            + f"goat_fed_republisher_core_endpoint: \"tls/{a.core_host}:7447\"\n"
            + "goat_fed_republisher_core_ca_src: \"{{ playbook_dir }}/../enrollment/ca/" + a.site + "/root-cert.pem\"\n"
            + "# One metrics port per peering on the shared core: 9310 + its slot.\n"
            + f"goat_fed_republisher_metrics_port: {a.metrics_port}\n"
        ))

    # The driver refuses a peering not declared in goatbuilder.yaml.
    gb_p = os.path.join(repo, "ops/goatbuilder/goatbuilder.yaml")
    gb = read(gb_p)
    if not re.search(rf"^- with: {re.escape(them)}$", gb, re.M):
        entry = (
            f"- with: {them}\n  environment: {a.gb_environment}\n  site: {a.gb_site}\n  edge_class: edge\n"
            f"  tunnel: wireguard\n  trust: spire-federation\n  ingress_prefix: from/{them}/\n"
        )
        if re.search(r"^peerings:\s*$", gb, re.M):
            gb = gb.rstrip("\n") + "\n" + entry
        else:
            gb = gb.rstrip("\n") + "\npeerings:\n" + entry
        write(gb_p, gb)


def set_edge(a):
    p = edge_vars(a.repo, a.them)
    write(p, set_scalar(read(p), a.key, a.value))


def forwarder_add(a):
    p = os.path.join(a.repo, "ops/ansible/group_vars/peering_forwarders/vars.yml")
    s = read(p)
    if re.search(rf"^\s*- name: {re.escape(a.them)}\s*$", s, re.M):
        return
    s = s.rstrip("\n") + (
        f"\n  - name: {a.them}\n"
        f"    edge_carrier_public_key: {q(a.edge_key)}\n"
        f"    interface: {a.carrier_iface}\n"
        f"    peering_port: {a.public_port}\n"
        f"    edge_peering_port: {a.listen_port}\n"
        f"    carrier_port: {a.carrier_port}\n"
        f"    carrier_local_ip: {a.carrier_net}.0\n"
        f"    carrier_remote_ip: {a.carrier_net}.1\n"
        "    partner_sources: []\n"
    )
    write(p, s)


def forwarder_key(a):
    """The forwarder's carrier key for this peering, from the play's output."""
    log = read(a.log)
    i = log.find(f"peering:   {a.them} (")
    if i < 0:
        sys.exit(f"the forwarder play printed nothing for {a.them}")
    m = re.search(r"peering_wg_forwarder_carrier_public_key: ([A-Za-z0-9+/]{43}=)", log[i:])
    if not m:
        sys.exit("no carrier key after the peering's line")
    print(m.group(1))


def wire_sets(us, releases):
    """from/<us>/<set>/** for each release/<set>/** a republisher lifts."""
    out = []
    for r in releases:
        m = re.fullmatch(r"release/([^/]+)/\*\*", r.strip())
        if m:
            out.append(f"from/{us}/{m.group(1)}/**")
    return out


def narrow_whole_prefix(text, us):
    """With several peerings goat-zenoh refuses one that reads all of from/<us>/**.
    Narrow each such entry to the wire keys of its own republisher's sets, with
    interest kept at from/<us>/**. Returns (text, [sovereigns narrowed])."""
    whole = f'["from/{us}/**"]'
    parts = re.split(r"(?m)^(?=  - sovereign:)", text)
    narrowed = []
    for i, part in enumerate(parts):
        m = re.match(r"  - sovereign:\s*(\S+)", part)
        if not m:
            continue
        rk = re.search(r"^(\s+release_key_exprs:\s*)(\[[^\]]*\])(.*)$", part, re.M)
        rp = re.search(r"^\s+republisher_release_key_exprs:\s*\[([^\]]*)\]", part, re.M)
        if not rk or rk.group(2).replace(" ", "") != whole.replace(" ", "") or not rp:
            continue
        sets = wire_sets(us, [x.strip().strip('"\'') for x in rp.group(1).split(",") if x.strip()])
        if not sets:
            continue
        line = rk.group(1) + "[" + ", ".join(q(x) for x in sets) + "]"
        part = part[:rk.start()] + line + part[rk.end():]
        if "release_interest_key_exprs" not in part:
            indent = re.match(r"\s*", rk.group(1)).group(0)
            part = part.replace(line, line + f"\n{indent}release_interest_key_exprs: [\"from/{us}/**\"]", 1)
        parts[i] = part
        narrowed.append(m.group(1))
    return "".join(parts), narrowed


def admit(a):
    repo, them, us = a.repo, a.them, a.us
    releases = [r for r in a.release.split(",") if r]
    sp_p = os.path.join(repo, "ops/ansible/group_vars", f"{a.site.replace('-', '_')}_hosts", "sovereign-peerings.yml")
    sp = read(sp_p) if os.path.exists(sp_p) else "zenoh_sovereign_peerings:\n"
    if not re.search(rf"^\s*- sovereign: {re.escape(them)}\s*$", sp, re.M):
        sp = re.sub(r"^(#.*\n)*", lambda m: m.group(0) + f"#   {them.upper()}  {a.digest}, through {a.expires}\n", sp, count=1) if a.digest else sp
        reads = wire_sets(us, releases) or [f"from/{us}/**"]
        sp = sp.rstrip("\n") + (
            f"\n  - sovereign: {them}\n"
            f"    cn: fed.{them}.{us}.01\n"
            f"    release_key_exprs: [{', '.join(q(r) for r in reads)}]\n"
            f"    release_interest_key_exprs: [\"from/{us}/**\"]\n"
            f"    republisher_cn: fed.{us}.{them}.republisher.01\n"
            f"    republisher_release_key_exprs: [{', '.join(q(r) for r in releases)}]\n"
        )
    sp, narrowed = narrow_whole_prefix(sp, us)
    if narrowed:
        print("narrowed to their own sets: " + ", ".join(narrowed))
    write(sp_p, sp)

    ev = edge_vars(repo, them)
    s = read(ev)
    if "goat_zenoh_edge_core_cn" not in s:
        image = first_match(repo, "*_edge", r"^goat_zenoh_edge_image:.*$")
        d = f"/etc/goat-zenoh-edge-{them}/tls"
        iface = re.search(r"^peering_wg_interface:\s*(\S+)", s, re.M)
        s = s.rstrip("\n") + (
            f"\n\n# --- Sovereign edge router for {them.upper()} (peering phase 12). Written by Federation.\n"
            + (image + "\n" if image else "# goat_zenoh_edge_image: set it, digest-pinned\n")
            + "goat_zenoh_edge_trust_mode: fragment\n"
            + f"# {them.upper()}'s CA only. Never a core trust file.\n"
            + f"goat_zenoh_edge_partner_ca_path: {d}/partner-ca.pem\n"
            + f"goat_zenoh_edge_our_ca_path: {d}/our-ca.pem\n"
            + f"goat_zenoh_edge_outward_cert: {d}/peering-outward.pem\n"
            + f"goat_zenoh_edge_outward_key:  {d}/peering-outward-key.pem\n"
            + f"goat_zenoh_edge_inward_cert:  {d}/peering-inward.pem\n"
            + f"goat_zenoh_edge_inward_key:   {d}/peering-inward-key.pem\n"
            + "goat_zenoh_edge_core_endpoints:\n"
            + f"  - \"tls/{a.core_host}:7447#root_ca_certificate_file={d}/our-ca.pem\"\n"
            + "goat_zenoh_edge_core_interface: wt0\n"
            + f"goat_zenoh_edge_partner_interface: {iface.group(1) if iface else 'wg-fed0'}\n"
            + "# Our core's router certificate CN. Without it default-deny drops everything on the core link.\n"
            + "goat_zenoh_edge_core_cn: goat-zenoh-router\n"
        )
        write(ev, s)

    # The review record: what apply rendered for this partner.
    for rel in (f"ops/federation/partners/{them}.yaml", f"ops/federation/acl/{them}-fragment.json5"):
        src = os.path.join(a.applied, rel)
        if os.path.exists(src):
            os.makedirs(os.path.dirname(os.path.join(repo, rel)), exist_ok=True)
            shutil.copyfile(src, os.path.join(repo, rel))


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("next-index")
    p.add_argument("--repo", required=True)
    p.add_argument("--them", required=True)

    p = sub.add_parser("declare")
    for k in ("repo", "them", "us", "site", "gb-environment", "gb-site", "edge-name", "edge-host", "edge-user", "core-name", "core-host", "core-user",
              "iface", "listen-port", "edge-port", "we-listen", "public-name", "carrier-port", "carrier-iface",
              "carrier-net", "carrier-local-port", "metrics-port", "waiver"):
        p.add_argument("--" + k, required=True)

    p = sub.add_parser("set-edge")
    p.add_argument("--repo", required=True)
    p.add_argument("--them", required=True)
    p.add_argument("--key", required=True)
    p.add_argument("--value", required=True)

    p = sub.add_parser("forwarder-add")
    for k in ("repo", "them", "edge-key", "carrier-iface", "public-port", "listen-port", "carrier-port", "carrier-net"):
        p.add_argument("--" + k, required=True)

    p = sub.add_parser("forwarder-key")
    p.add_argument("--log", required=True)
    p.add_argument("--them", required=True)

    p = sub.add_parser("admit")
    for k in ("repo", "them", "us", "site", "core-host", "applied", "release"):
        p.add_argument("--" + k, required=True)
    p.add_argument("--digest", default="")
    p.add_argument("--expires", default="")

    a = ap.parse_args()
    global US_LABEL
    if getattr(a, "us", None):
        US_LABEL = a.us.upper()
    if not re.fullmatch(r"[a-z0-9][a-z0-9-]{1,15}", a.them):
        sys.exit("bad sovereign id")
    {"next-index": next_index, "declare": declare, "set-edge": set_edge, "forwarder-add": forwarder_add,
     "forwarder-key": forwarder_key, "admit": admit}[a.cmd](a)


if __name__ == "__main__":
    main()
