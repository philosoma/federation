# Shared by every phase script. Sourced, not run.
#
# Values come from the environment, set by the Federation app (FED_THEM and
# friends) and by the container (where the secrets and the hosts are). Nothing
# here takes a value from a page field into a command line except through a
# variable, and the app validates the ones that come from people.
set -euo pipefail
umask 077

: "${FED_US:?}" "${FED_THEM:?}" "${FED_WORK:?}" "${FED_STATE:?}" "${FED_SCRIPTS:?}"
: "${FED_SECRETS:=/run/secrets/federation}"
: "${FED_DIRECTORY_URL:=https://saltlick.90at.net}"
: "${FED_GOATCORE_REPO:=https://github.com/USAREUR-AF/goatcore.git}"
: "${FED_GOATSTUFF_REPO:=https://github.com/USAREUR-AF/goatstuff.git}"
# Site settings, from the deploy role (see README). No defaults: a missing one
# stops the phase with its name rather than reaching for another node's host.
: "${FED_SITE:?set FED_SITE, the goatnet site id, as in group_vars/<site>_hosts}"
: "${FED_CONFIG_REPO:?set FED_CONFIG_REPO, the git URL of your goatnet config overlay}"
: "${FED_CONTACT:?set FED_CONTACT, the email partners use to reach you about this link}"
: "${FED_EDGE_NAME:?set FED_EDGE_NAME, the edge host's inventory name}"
: "${FED_EDGE_HOST:?set FED_EDGE_HOST, the edge host's mesh address}"
: "${FED_EDGE_USER:?set FED_EDGE_USER, the edge's SSH login}"
: "${FED_CORE_NAME:?set FED_CORE_NAME, the core host's inventory name}"
: "${FED_CORE_HOST:?set FED_CORE_HOST, the core host's mesh address}"
: "${FED_CORE_USER:?set FED_CORE_USER, the core's SSH login}"
: "${FED_PUBLIC_NAME:=}"        # the forwarder's name partners dial; needed only when we listen
: "${FED_FIPS_WAIVER:=FIPS 140 not required for this link}"
: "${FED_GB_ENVIRONMENT:=$FED_SITE}"    # goatbuilder.yaml environment and site for a declared peering
: "${FED_GB_SITE:=$FED_SITE-a}"

THEM=$FED_THEM
US=$FED_US
W=$FED_WORK
REPOS=$FED_STATE/repos
CFG=$REPOS/config
STUFF=$REPOS/goatstuff
CORE_MIRROR=$REPOS/goatcore.git
CTX=$FED_SITE-$THEM
EDGE=$FED_EDGE_USER@$FED_EDGE_HOST
CORE=$FED_CORE_USER@$FED_CORE_HOST
SEED=$FED_SECRETS/signing.seed
CA_KEY=$FED_SECRETS/ca-key.pem
export HOME=$FED_STATE/home
export GOAT_CONTEXT=$CTX
export GIT_TERMINAL_PROMPT=0
export ANSIBLE_STDOUT_CALLBACK=default ANSIBLE_FORCE_COLOR=0 ANSIBLE_NOCOLOR=1
export ANSIBLE_SSH_RETRIES=5 ANSIBLE_HOST_KEY_CHECKING=False
export GOAT_SECRET_STORE=env-file GOAT_AWS_MODE=profile
export AWS_SHARED_CREDENTIALS_FILE=$FED_SECRETS/aws-credentials AWS_DEFAULT_REGION=eu-west-2

# step: a named stage of the phase. The page shows "N of STEPS" from these;
# each script sets STEPS to the number it expects.
STEP_N=0
: "${STEPS:=0}"
step() {
	STEP_N=$((STEP_N + 1))
	[ "$STEP_N" -le "$STEPS" ] || STEPS=$STEP_N
	echo "::progress $STEP_N/$STEPS $*"
	printf '\n── %s\n' "$*"
}
# die: the phase failed. The message is the one line the page shows.
die() { echo "::note $*"; echo "ERROR: $*" >&2; exit 1; }
# waiting: not a failure; something outside has to happen first.
waiting() { echo "::note $*"; exit 75; }
show() { echo "::show $1=$2"; }
need() { [ -r "$1" ] || die "Missing $2 ($1). Federation's deploy installs it."; }

# Retries for SSH: the edge rate-limits a burst of connections.
ssh_edge() { local i; for i in 1 2 3 4 5; do ssh "$EDGE" "$@" && return 0; local rc=$?; [ $rc -eq 255 ] || return $rc; sleep 5; done; return 255; }
ssh_core() { ssh "$CORE" "$@"; }

# ---- home: SSH, git, goat context ----------------------------------------
setup_home() {
	mkdir -p "$HOME/.ssh" "$HOME/.goat/contexts" "$HOME/.goat/federation"
	need "$FED_SECRETS/ssh-key" "the SSH key for the edge and core"
	cat >"$HOME/.ssh/config" <<-EOF
		Host $FED_EDGE_HOST $FED_CORE_HOST
		  IdentityFile $FED_SECRETS/ssh-key
		  IdentitiesOnly yes
		Host github.com
		  IdentityFile $FED_SECRETS/github-key
		  IdentitiesOnly yes
		Host *
		  UserKnownHostsFile $FED_STATE/known_hosts
		  StrictHostKeyChecking accept-new
		  BatchMode yes
		  ConnectTimeout 10
		  ServerAliveInterval 20
	EOF
	need "$FED_SECRETS/github-key" "the deploy key for the config overlay"
	# The goat context: the operator profile from the secrets, plus this
	# peering's own federation.toml (a context holds one [transport]).
	need "$FED_SECRETS/goat-context/profile.toml" "the goat operator profile"
	mkdir -p "$HOME/.goat/contexts/$CTX"
	local f
	for f in "$FED_SECRETS"/goat-context/*; do
		[ "$(basename "$f")" = federation.toml ] && continue
		cp -f "$f" "$HOME/.goat/contexts/$CTX/"
	done
	need "$SEED" "our federation signing key"
	ln -sf "$SEED" "$HOME/.goat/federation/$US-signing.seed"
	git config --global user.name "Federation ($FED_SITE)"
	git config --global user.email "$FED_CONTACT"
	git config --global pull.rebase true
}

# ---- repositories --------------------------------------------------------
sync_repos() {
	mkdir -p "$REPOS"
	step "Fetching the config, goatcore and goatstuff"
	if [ -d "$CFG/.git" ]; then
		git -C "$CFG" fetch -q origin
	else
		git clone -q "$FED_CONFIG_REPO" "$CFG"
	fi
	git -C "$CFG" checkout -q -B main origin/main
	git -C "$CFG" reset -q --hard origin/main
	git -C "$CFG" clean -qfdx
	if [ -d "$CORE_MIRROR" ]; then git -C "$CORE_MIRROR" fetch -q origin; else git clone -q --mirror "$FED_GOATCORE_REPO" "$CORE_MIRROR"; fi
	if [ -d "$STUFF/.git" ]; then git -C "$STUFF" fetch -q origin; else git clone -q "$FED_GOATSTUFF_REPO" "$STUFF"; fi
	echo "config main at $(git -C "$CFG" rev-parse --short HEAD)"
}

# Commit what changed in the config and push it to main. Another session may
# have pushed meanwhile: rebase and try again.
push_config() {
	git -C "$CFG" add -A
	if git -C "$CFG" diff --cached --quiet; then
		echo "config unchanged"
		return 0
	fi
	git -C "$CFG" commit -q -m "$1" -m "Written by Federation for the $US-$THEM peering."
	local i
	for i in 1 2 3; do
		if git -C "$CFG" push -q origin HEAD:main; then
			echo "pushed config main at $(git -C "$CFG" rev-parse --short HEAD): $1"
			return 0
		fi
		git -C "$CFG" pull -q --rebase origin main || die "Could not rebase the config onto GitHub's main. Someone changed the same lines."
	done
	die "Could not push the config to GitHub."
}

# A fresh deploy tree from the config as committed (goatbuilder refuses a tree
# edited after assembly). Keeps the last three for this peering.
assemble() {
	step "Assembling a deploy tree from config main"
	mkdir -p "$FED_STATE/trees"
	TREE=$FED_STATE/trees/$THEM-$(date -u +%Y%m%d%H%M%S)
	GOATCORE_REPO=$CORE_MIRROR GOATSTUFF_DIR=$STUFF "$CFG/assemble.sh" "$TREE"
	mkdir -p "$TREE/.envrc.local.d"
	ln -sf "$FED_SECRETS/secrets.env" "$TREE/.envrc.local.d/secrets.env"
	echo "$TREE" >"$W/tree"
	# shellcheck disable=SC2012
	ls -1dt "$FED_STATE/trees/$THEM-"* 2>/dev/null | tail -n +4 | xargs -r rm -rf
}

# ---- this peering's resources --------------------------------------------
# Peering number N gets wg-fedN, edge port 5183N, link 169.254.(20+N).0/31,
# republisher metrics port 9310+N and, through the forwarder, public port
# 51820+2N with its carrier on the next port. Every edge router listens on
# 7448, the port partners dial (it isn't in the agreement): each binds its own
# link address, so they don't clash. Carriers live in 169.254.(200+N), away
# from the link subnets partners propose. Kept in $W/alloc.env once chosen.
alloc() {
	if [ ! -f "$W/alloc.env" ]; then
		echo "Choosing this link's interface, ports and subnet"
		local n
		n=$(python3 "$FED_SCRIPTS/fedcfg.py" next-index --repo "$CFG" --them "$THEM")
		cat >"$W/alloc.env" <<-EOF
			N=$n
			IFACE=wg-fed$n
			LISTEN_PORT=$((51830 + n))
			EDGE_PORT=7448
			METRICS_PORT=$((9310 + n))
			SUBNET=169.254.$((20 + n)).0/31
			PUBLIC_PORT=$((51820 + 2 * n))
			CARRIER_PORT=$((51821 + 2 * n))
			CARRIER_IFACE=wg-carry$n
			CARRIER_NET=169.254.$((200 + n))
			CARRIER_LOCAL_PORT=$([ "$n" -eq 0 ] && echo 51822 || echo $((51840 + n)))
		EOF
	fi
	# shellcheck disable=SC1091
	. "$W/alloc.env"
	# Older allocations had no metrics port (slot 0 uses the default 9310), and
	# gave each router its own port: every router now listens on 7448.
	: "${METRICS_PORT:=$((9310 + N))}"
	EDGE_PORT=7448
	echo "link $N: $IFACE, edge port $LISTEN_PORT, router port $EDGE_PORT, subnet $SUBNET"
}

federation_toml() {
	cat >"$HOME/.goat/contexts/$CTX/federation.toml" <<-EOF
		# Written by Federation for $US <-> $THEM.
		sovereign_id = "$US"
		term_days    = 91
		cut_contact  = "$FED_CONTACT"

		[transport]
		interface            = "$IFACE"
		endpoint             = "${FED_PUBLIC_NAME:-unset.invalid}:$PUBLIC_PORT"
		listener             = $FED_WE_LISTEN
		proposed_link_subnet = "$SUBNET"
		measured_mtu         = 1300

		[data]
		release                  = ["release/REL-ALL/**"]
		accept_hz                = 100
		accept_bytes_per_message = 1048576
		accept_distinct_keys     = 5000
	EOF
}

# ---- Ansible -------------------------------------------------------------
# A peering play, the way the driver runs it.
play() {
	local limit=$1 pb=$2
	shift 2
	step "ansible-playbook $pb (limit $limit)"
	(cd "$TREE/ops/ansible" && ../scripts/with-secrets ansible-playbook \
		-i inventory/peering-edges.ini --limit "$limit" \
		-e "fed_edge_group=${THEM}_edge" -e "fed_work=$W" -e "fed_counterparty=$W/theirs.canonical.json" \
		"$@" "$pb")
}

# Keep an older copy of a file or folder before a phase replaces it.
keep_old() {
	local stamp
	stamp=$(date -u +%Y%m%d%H%M%S)
	local f
	for f in "$@"; do
		[ -e "$W/$f" ] || continue
		mkdir -p "$W/old-$stamp"
		mv "$W/$f" "$W/old-$stamp/"
	done
}

# Our CA certificate in an assembled tree.
our_ca_cert() { echo "$TREE/ops/enrollment/ca/$FED_SITE/root-cert.pem"; }

groups4() { echo "$1" | sed -E 's/(.{4})/\1 /g; s/ $//'; }
