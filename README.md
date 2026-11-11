# Federation

> **Early and rough.** This is the tool one goatnet (EFDI UK) built to federate
> with partner nations. It has completed two live peerings, but it was written for one
> site and has only been tested there. Expect to read the scripts before you trust them,
> and expect sharp edges. Issues and fixes are welcome.

Federation is a small web app that runs on your goatnet core host. It takes an
operator through the goatcore cross-sovereign federation process (ADR 1157)
with a partner nation, one phase at a time. There is one page, with a **Run**
button per phase. Each phase runs the goat, goatbuilder and Ansible steps
behind the scenes and shows its log in an expandable section. Waits for the
partner are shown as waits, not failures. A **Status** page shows every partner
and whether its link is up, and links to their data in Flashlight.

## The phases

| Row | Phases | What it does | Both sides at once? |
|---|---|---|---|
| 1 | 1 | Preflight; allocate this link's slot (interface, ports, link subnet) and declare it in the config | |
| 2 | 2–4 | Prepare the edge, build and lint our package, publish it to the directory | |
| 5 | 5 | Exchange: fetch theirs, read both fingerprints to each other, tick ours and theirs | yes (a call) |
| 6 | 6–9 | Compose and verify the agreement, mint the peering certificates, render the applied files | |
| 10 | 10 | Admit the peering on our core router (asks first: it restarts the router) | |
| 11 | 11 | Bring up the WireGuard tunnel (direct, or through the peering forwarder) | yes |
| 12 | 12 | Edge router and republisher (asks first); waits for their edge to connect | yes |
| 13 | 13 | SPIRE trust-domain federation: **not done**, optional | |
| 14 | 14 | Acceptance round trip; waits for them to confirm theirs | yes |

Each phase commits its changes to your config overlay and pushes them to main.
It then deploys from a freshly assembled tree, so the repository stays the
record.

## What you need

- A goatnet core built from goatcore, with a config overlay repository (the
  `assemble.sh` layout) and a peering edge host in `inventory/peering-edges.ini`.
- **goatcore with `goat init --over-ssh` and the SPIRE trust-domain fix.** At the
  time of writing these are on goatcore branch `integration/federation-turnkey`,
  not `main`. The image builds goat and goatbuilder from a `git archive` of that
  source, saved as `goatcore-src.tar.gz` next to the Dockerfile. It is not in
  this repository.
- oauth2-proxy and Hydra on the core, as Signpost uses (the role adds its own
  login door). The app refuses requests without an allowed group, and listens
  only on loopback if no groups are set.
- Your federation signing seed, your site CA key and the goat operator profile
  on the machine you deploy from. The role **copies the CA key onto the core**
  so phases can mint certificates unattended. Decide whether your key policy
  allows that.
- If your edge is behind NAT and you listen: the AWS peering forwarder (ADR 1218)
  and a limited IAM user for it, given as `FEDERATION_AWS_ACCESS_KEY_ID` and
  `FEDERATION_AWS_SECRET_ACCESS_KEY` in the deploy environment.
- From the core to the edge: SSH reachability over your mesh. The play adds
  the app's key and a passwordless sudo rule for the edge login.

## Settings

Set these in `group_vars/<site>_hosts/federation.yml` of your config overlay.
The role refuses to run without the required ones.

| Setting | Required | Example |
|---|---|---|
| `federation_sovereign_id` | yes | `gbr` |
| `federation_edge_name` | yes | `edge-1` (inventory name) |
| `federation_edge_host` | yes | `100.64.0.20` (mesh address) |
| `federation_edge_user` | yes | `ops` |
| `federation_config_repo` | yes | `git@github.com:you/your-goatnet-config.git` |
| `federation_contact` | yes | `federation@example.org` (goes in your package) |
| `federation_public_name` | if you listen | `fed.example.org` (the forwarder's name) |
| `federation_site_name`, `federation_site_flag` | | `Example`, `gb` (ISO alpha-2) |
| `federation_fips_waiver` | | the waiver text phase 2 records |
| `federation_core_name/host/user` | | default: `inventory_hostname`, `mesh_ip_a`, `ansible_user` |
| `federation_allowed_groups` | | `PLATFORM-ADMIN` |
| `federation_image` | yes | `ghcr.io/<owner>/federation@sha256:…` |

The role passes them into the container as `FED_*` variables. The phase
scripts stop with the variable's name if one is missing. See
`deploy/ansible/roles/federation/defaults/main.yml` for the rest.

## Build and deploy

```sh
go test ./...
# on the core (the image is built for its architecture):
cp /path/to/goatcore-src.tar.gz .
docker build --build-arg VERSION=$(git rev-parse --short HEAD) -t federation:dev .
# push it to your registry, then pin the digest in federation_image
```

Copy `deploy/ansible/federation.yml` and `deploy/ansible/roles/federation` into
your overlay's `ops/ansible`. Then run from an assembled tree:

```sh
ansible-playbook -i inventory/mgmt-hosts.ini -i inventory/peering-edges.ini \
  federation.yml -e target=<site>_hosts
```

The play prints a GitHub deploy key. Add it to your config overlay with write
access, or phase 1 can't push.

For local development, `go run ./cmd/federation` serves the page on
`127.0.0.1:8090` with the state in `./data`. Phases need the core's environment,
so they won't get far locally.

## How it works

- `cmd/federation`: the server. The config is `examples/config.example.json`.
- `internal/runner/phases.go` lists the phases. `internal/runner/scripts/` holds
  one bash script per row, plus `lib.sh` and `fedcfg.py`, which edits the config
  overlay. Scripts talk to the page through stdout lines: `::show k=v`,
  `::note`, `::progress N/M label`. Exit 75 means "waiting for the partner".
- Only one phase runs at a time. The state, logs and work files live under
  `/var/lib/federation`, and each peering's slot is in `work/<them>/alloc.env`.
- Slot N gives you `wg-fedN`, edge port `51830+N`, public port `51820+2N` on
  the forwarder, link subnet `169.254.(20+N).0/31` (unless the partner's wins)
  and republisher metrics port `9310+N`. Every edge router listens on 7448.

## Known gaps

- Written against one site's layout: inventory names, group_vars paths and the
  forwarder path all follow EFDI UK's overlay. Other layouts will need changes
  in `fedcfg.py` and `lib.sh`.
- Phase 13 (SPIRE federation) is not implemented.
- There is no "remove peering" button. Removing a peering means editing the config, the
  forwarder and the edge by hand.
- The directory holds one package per sovereign, and the app doesn't warn when
  another peering's exchange is still open.
- Agreements expire (91 days by default). Renewal is a new exchange, and the app
  doesn't remind you.
- No CI or published image yet, because the goatcore source it needs isn't on
  goatcore `main`.

## Licence

Apache-2.0, see `LICENSE`. Country flags in `internal/server/static/flags` are
from flag-icons (MIT, `third_party/flag-icons/`). `internal/server/countries.go`
is generated from Debian's iso-codes. The page design is adapted from the goat
identity console in goatcore (Apache-2.0, `third_party/goatcore/`).
