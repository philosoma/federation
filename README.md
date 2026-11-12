# Federation

> **Early and rough.** One goatnet (EFDI UK) built this to federate with partner
> nations. It was rebuilt on 2026-11-12 around `goatbuilder federate`
> (goatbuilder 0.5.1), and that path has not yet completed a peering between two
> standing environments. Read the scripts before you trust them. Issues and
> fixes are welcome.

Federation is a small web app that runs on your goatnet core host. It walks an
operator through federating with another nation, following goatcore's operator
guide, *Federate two sovereign environments with goatbuilder*. You decide who
the partner is and what they get, confirm the keys with them on a call, and
approve what your core admits. `goatbuilder federate` does, checks and records
the rest; the app runs it for you and shows what it needs from you.

## What you see

**Setup** is one guided list per partner. Finished steps collapse to a line,
the current step is open with its one button, and later steps wait.

| Step | goatbuilder | What happens | With the partner? |
|---|---|---|---|
| Check we're ready | (before) | Keys, config and push access, what we offer, the directory, the edge, the core router, the forwarder. Changes nothing. | |
| Reserve the link | `allocate` | Declares the peering and lets goatbuilder choose its interface, ports and addresses. Commits them. Shows the address they dial, if we listen. | |
| Exchange packages and read the keys | phases 1–5 | Measures and signs our package, swaps packages through the directory. Shows our codes to read aloud and their CA fingerprint to check; you type their key. | on a call |
| Agree | phase 6 | Composes the agreement against the confirmed keys. Shows the digest to read aloud. | on a call |
| Review the plan | phases 7–9, `plan` | Mints certificates, renders everything, writes the bounded plan and what the core will admit. Changes no host. You approve both. | |
| Deploy | phases 10–13 | Admits the edge on the core (the router restarts), opens the tunnel, starts the edge router and republisher. | same time |
| Prove it | phase 14 | Acceptance from our side, each check listed. Counts only on PASS. | same time |

A step that fails shows the one-line reason, what `goatbuilder explain` says
about its error code, the log, and a **Try again** button: goatbuilder resumes
where it stopped. A step that is waiting on the partner says so.

**Status** shows every partner the core admits: tunnel, traffic, edge router,
republisher, agreement end date, links to their data, and **What we send**.

## Choosing what each partner gets

Each partner's card on Status has a **What we send** box with one checkbox per
topic. Saving is a release decision: the app commits it to the release file in
your config overlay (`ops/release-bridge/release-bridge.json`, or
`FED_RELEASE_FILE`) and restarts the release bridge on the core. Nothing
restarts on the router and no other partner is touched.

```json
{
  "rest": "http://127.0.0.1:8000",
  "set": "REL-ALL",
  "topics": [
    {"id": "turret", "name": "Sentry turret", "src": ["<slot>/turret/cots"]},
    {"id": "sattrack", "name": "Sat tracker", "src": ["<slot>/sattrack/cots"]}
  ],
  "send": {"can": ["turret", "sattrack"], "est": ["turret"]}
}
```

The bridge copies each sample to `release/<set>/<partner>/<key without its slot
id>`. Each partner's read on the core is its own prefix
(`from/<us>/<set>/<partner>/**`) plus the acceptance keys, so it gets only what
is ticked for it. goatbuilder's admission writes only the partner's name and CN
on the core; the app writes these reads before the plan.

## What you need

- A goatnet built from goatcore at or after tag `goatbuilder/v0.5.1`, with a
  config overlay (the `assemble.sh` layout) the app can push to.
- A peering edge host on your mesh, reachable by SSH from the core.
- oauth2-proxy and Hydra on the core, as Signpost uses. The app refuses
  requests without an allowed group, and listens only on loopback if none is set.
- Your federation signing seed, your site CA key and the goat operator profile
  on the machine you deploy from. The role **copies the CA key onto the core**
  so certificates can be minted unattended. Decide whether your key policy
  allows that.
- If partners can't accept inbound traffic and neither can your edge: a peering
  forwarder (ADR 1218), its group in `inventory/peering-edges.ini` (goatbuilder
  passes only that inventory), and a limited IAM user for it
  (`FEDERATION_AWS_ACCESS_KEY_ID` / `FEDERATION_AWS_SECRET_ACCESS_KEY` at deploy).

In the config overlay:

- `ops/federation/<environment>/federation.toml`: what you offer
  (`sovereign_id`, `term_days`, `cut_contact`, `[data] release`,
  `[transport] measured_mtu`).
- `ops/goatbuilder/goatbuilder.yaml`: `environments[].peering_directory.url`.
  The app adds each `peerings:` entry itself.
- `group_vars/<site>_hosts/federation-site.yml`: `goat_zenoh_edge_core_cn`,
  `goat_fed_republisher_core_endpoint`, `peering_spire_server_addr`. The two
  images (`goat_fed_republisher_image`, `goat_zenoh_edge_image`) are read from
  your generated pins or an existing peering's half; for a first peering, set
  them there.
- `group_vars/<site>_hosts/peerings.yml` for `zenoh_sovereign_peerings`:
  goatbuilder's admission refuses any other file (GB-237).

## Settings

Set these in `group_vars/<site>_hosts/federation.yml`. The role refuses to run
without the required ones.

| Setting | Required | Example |
|---|---|---|
| `federation_sovereign_id` | yes | `gbr` |
| `federation_edge_name` | yes | `edge-1` (inventory name) |
| `federation_edge_host` | yes | `100.64.0.20` (mesh address) |
| `federation_edge_user` | yes | `ops` |
| `federation_config_repo` | yes | `git@github.com:you/your-goatnet-config.git` |
| `federation_contact` | yes | `federation@example.org` |
| `federation_public_name` | if you listen | `fed.example.org` (the forwarder's name) |
| `federation_site_name`, `federation_site_flag` | | `Example`, `gb` |
| `federation_fips_waiver` | | the waiver text written into each edge half |
| `federation_core_name/host/user` | | default: `inventory_hostname`, `mesh_ip_a`, `ansible_user` |
| `federation_allowed_groups` | | `PLATFORM-ADMIN` |
| `federation_image` | yes | `ghcr.io/<owner>/federation@sha256:…` |

## Build and deploy

```sh
go test ./...
docker build --build-arg VERSION=$(git rev-parse --short HEAD) -t federation:dev .
```

The image downloads the released goatbuilder and checks it against the pinned
digest (arm64 and amd64). Push it to your registry and pin its digest in
`federation_image`. Copy `deploy/ansible/federation.yml` and
`deploy/ansible/roles/federation` into your overlay's `ops/ansible`, then from
an assembled tree:

```sh
ansible-playbook -i inventory/mgmt-hosts.ini -i inventory/peering-edges.ini \
  federation.yml -e target=<site>_hosts
```

The play prints a GitHub deploy key. Add it to your config overlay with write
access. `go run ./cmd/federation` serves the page on `127.0.0.1:8090` for
development; the steps need the core's environment.

## How it works

- `internal/runner/phases.go` lists the steps; `internal/runner/scripts/gb-*.sh`
  is one script per step. Scripts talk to the page through stdout lines:
  `::show key=value`, `::note`, `::progress N/M label`. Exit 75 means "waiting
  on the partner".
- Each step assembles a deploy tree from config main and runs
  `goatbuilder federate … -C <tree>`, with the signing key, CA and directory
  passed as `FED_*` overrides. Deploy and acceptance reuse the tree the plan
  was made from, or the plan's approval no longer applies.
- goatbuilder renders files and never commits; the app copies the named files
  back into the overlay and pushes them, so the repository stays the record.
- goatbuilder's work directory for a peering is `<state>/gb/<partner>`: its
  journal, both packages, the agreement, `plan.json`, the acceptance evidence.
- Peerings are declared `deployment: hand`: goatbuilder's rendered half turns
  SPIRE federation on. The app writes the small hand half
  (`group_vars/<partner>_{edge,core}/vars.yml`) and goatbuilder renders the
  allocated values beside it (`peering-values.yml`).
- When you listen through a forwarder, the forwarder's list reads each
  carrier's public keys from files in the work directory. The app keeps them in
  the overlay (`ops/federation/<environment>/carrier-keys/`) and puts them in
  place before every run: a missing file would drop that peering from the list.
- One step runs at a time for the whole node.

## Known gaps

- The goatbuilder path is new here: expect fixes after the first real peerings.
- SPIRE federation (phase 13) is left off and reports NOT DONE, so acceptance
  ends INCOMPLETE rather than PASS; so do the identity and refusal checks,
  which need a probe launcher goatcore doesn't ship yet.
- The shared acceptance session (both halves combined, then "exchanging"
  published) is not driven from the page. Its commands are in goatcore's
  `federation-acceptance.md`, section 6.
- Renewing a running peering in place is untested upstream. New certificates
  need `mint-peering-leaves.sh --rotate`, which the page does not run.
- Backout is offered (survey first), but goatbuilder has not run it on real
  hosts.
- The directory holds one package per party: one exchange at a time.
- Written against one site's overlay layout.

## Licence

Apache-2.0, see `LICENSE`. Country flags in `internal/server/static/flags` are
from flag-icons (MIT, `third_party/flag-icons/`). `internal/server/countries.go`
is generated from Debian's iso-codes. The page design is adapted from the goat
identity console in goatcore (Apache-2.0, `third_party/goatcore/`).
