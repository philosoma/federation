#!/usr/bin/env python3
"""What two packages, or the agreement composed from them, actually carry.

  offers.py package <theirs.canonical.json> <name>
      prints a line per thing their package lacks (nothing when it is complete)
  offers.py agreement <agreement.json> <us> <them> <their name>
      prints "ok <summary>" when the agreement has a transport and a flow each
      way; "warn <text>" for one direction only; "fail <text>" when it would
      federate nothing. Exit 0 always: the caller decides.
"""
import json
import sys


def load(p):
    with open(p, encoding="utf-8") as f:
        return json.load(f)


def package(path, name):
    p = load(path)
    t, d = p.get("transport_offer") or {}, p.get("data_offer") or {}
    if not t:
        print(f"{name}'s package has no transport offer (no tunnel key, subnet or MTU): no tunnel can be agreed.")
    if not d.get("release"):
        print(f"{name}'s package offers no release sets: nothing would flow from them to us.")
    if not any(k.startswith("accept_") for k in d):
        print(f"{name}'s package accepts nothing: nothing would flow from us to them.")


def agreement(path, us, them, name):
    a = load(path)
    flows = a.get("flows") or []
    out = [f for f in flows if f.get("from") == us and f.get("to") == them]
    back = [f for f in flows if f.get("from") == them and f.get("to") == us]
    t = a.get("transport") or {}
    if not t:
        print(f"fail The agreement has no transport, so there would be no tunnel. One package carries no transport offer: check {name}'s first. Don't read this digest: fix the packages and exchange again.")
        return
    if not out and not back:
        print(f"fail The agreement has no data flow in either direction. A flow needs one side to offer release sets and the other to accept them. Don't read this digest: fix the packages and exchange again.")
        return
    sets = lambda fl: ", ".join(r.replace("release/", "").replace("/**", "") for f in fl for r in f.get("release", [])) or "nothing"
    listener = t.get("listener", "?")
    summary = f"{t.get('kind', '?')}, {listener} listens, link {t.get('link_subnet', '?')}; we send {sets(out)}; they send {sets(back)}"
    if not out or not back:
        print(f"warn One direction only: we send {sets(out)}; they send {sets(back)}. Confirm with {name} that this is what you both intend before reading the digest.")
    print("ok " + summary)


if __name__ == "__main__":
    {"package": package, "agreement": agreement}[sys.argv[1]](*sys.argv[2:])
