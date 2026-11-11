#!/usr/bin/env python3
"""release-bridge: copy chosen internal topics to each partner's release prefix.

Only data staged under release/<set>/** crosses a sovereign peering: the
republisher lifts it onto from/<us>/<set>/**. Each partner's core read is
narrowed to its own prefix, from/<us>/<set>/<them>/**, so what is staged under
release/<set>/<them>/ reaches that partner and nobody else.

This streams each topic's source prefixes from the core router's local REST API
(127.0.0.1, server-sent events) and puts every sample under the prefix of each
partner the topic is sent to. The key keeps everything after its first segment
(the publisher's slot id):

    916c.../turret/cots/v1  ->  release/REL-ALL/can/turret/cots/v1

What is listed under "send" is a release decision. Federation's Status page
edits it, one checkbox per partner and topic, and commits it to the config.

Config (JSON):
  {"rest": "http://127.0.0.1:8000", "set": "REL-ALL",
   "topics": [{"id": "turret", "name": "Sentry turret", "src": ["<slot>/turret/cots", ...]}],
   "send": {"can": ["turret"], "est": []}}
"""
import base64, json, re, sys, threading, time, urllib.request

CFG = json.load(open(sys.argv[1]))
REST = CFG.get("rest", "http://127.0.0.1:8000").rstrip("/")
SET = CFG.get("set", "REL-ALL")
NAME = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$")


def put(key, body, ctype):
    req = urllib.request.Request(f"{REST}/{key}", data=body, method="PUT",
                                 headers={"Content-Type": ctype})
    urllib.request.urlopen(req, timeout=5).close()


def body_of(s):
    enc, val = s.get("encoding") or "application/octet-stream", s.get("value")
    if enc.startswith("application/json"):
        return json.dumps(val, separators=(",", ":")).encode(), enc
    if isinstance(val, str):
        try:
            return base64.b64decode(val, validate=True), enc
        except Exception:
            return val.encode(), enc
    return json.dumps(val).encode(), enc


def relay(src, dsts):
    """Stream src/** and put each sample under every dst prefix."""
    while True:
        try:
            req = urllib.request.Request(f"{REST}/{src}/**", headers={"Accept": "text/event-stream"})
            with urllib.request.urlopen(req, timeout=60) as r:
                print(f"streaming {src}/** -> {', '.join(d + '/**' for d in dsts)}", flush=True)
                event = None
                for raw in r:
                    line = raw.decode("utf-8", "replace").rstrip("\r\n")
                    if line.startswith("event:"):
                        event = line[6:].strip()
                    elif line.startswith("data:") and event == "PUT":
                        s = json.loads(line[5:])
                        key = s["key"]
                        if not key.startswith(src + "/"):
                            continue
                        body, enc = body_of(s)
                        for dst in dsts:
                            put(dst + key[len(src):], body, enc.split(";")[0])
        except Exception as e:  # router restart, timeout: back off and resubscribe
            print(f"{src}: {e!r}; retrying in 5s", file=sys.stderr, flush=True)
            time.sleep(5)


def routes():
    """src prefix -> the partner prefixes it is copied to."""
    out = {}
    topics = {t["id"]: t for t in CFG.get("topics", [])}
    for them, ids in sorted(CFG.get("send", {}).items()):
        if not NAME.match(them):
            sys.exit(f"bad partner code {them!r}")
        for tid in ids:
            if tid not in topics:
                sys.exit(f"{them}: unknown topic {tid!r}")
            for src in topics[tid]["src"]:
                src = src.strip("/")
                rest = src.split("/", 1)[1] if "/" in src else src
                out.setdefault(src, []).append(f"release/{SET}/{them}/{rest}")
    # The older layout: one list of src -> dst, shared by every partner.
    for m in CFG.get("map", []):
        out.setdefault(m["src"].rstrip("/"), []).append(m["dst"].rstrip("/"))
    return out


R = routes()
if not R:
    print("nothing is released: idle", flush=True)
    while True:
        time.sleep(3600)
threads = [threading.Thread(target=relay, args=(src, dsts), daemon=True) for src, dsts in R.items()]
for t in threads:
    t.start()
for t in threads:
    t.join()
