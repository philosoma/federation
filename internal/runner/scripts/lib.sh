# Shared by every step script. Sourced, not run.
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

# Our CA certificate in an assembled tree.
our_ca_cert() { echo "$TREE/ops/enrollment/ca/$FED_SITE/root-cert.pem"; }

groups4() { echo "$1" | sed -E 's/(.{4})/\1 /g; s/ $//'; }

# ---- goatbuilder federate (goatbuilder 0.5.1 and later) -------------------
# Its own work directory per peering, kept across runs so a stopped run resumes.
GBW=$FED_STATE/gb/$THEM
# gb: goatbuilder federate in the assembled tree, with our keys and CA passed as
# overrides (the declarations in goatbuilder.yaml name the operator laptop's).
gb() {
	mkdir -p "$GBW"
	seed_carrier_keys
	(cd "$TREE" && FED_WORK="$GBW" FED_WORK_ROOT="$FED_STATE/gb" \
		FED_SIGNING_KEY="$SEED" FED_CA_KEY="$CA_KEY" FED_CA_CERT="$(our_ca_cert)" \
		FED_DIRECTORY_URL="$FED_DIRECTORY_URL" GOAT_PROFILE_DIR="$HOME/.goat" \
		goatbuilder federate "$@" -C "$TREE" --color never)
}
# The tree the plan was made from: deploy and test must use the same one, or
# the plan's inputs have moved and the approval no longer applies.
use_plan_tree() {
	TREE=$(cat "$W/tree" 2>/dev/null) && [ -d "$TREE" ] || die "Make the plan first."
}
# Bring the files goatbuilder wrote in the tree back into the config, so they're
# committed there: it renders, it never commits. Named paths only: the tree
# also holds goatcore and goatstuff files that are not ours to commit.
tree_to_config() {
	local f n=0
	for f in ops/goatbuilder/peering-values.yaml \
		ops/ansible/group_vars/"${THEM}"_edge/peering-values.yml ops/ansible/group_vars/"${THEM}"_core/peering-values.yml \
		ops/ansible/group_vars/"${THEM}"_edge/peering-deployment.yml ops/ansible/group_vars/"${THEM}"_core/peering-deployment.yml \
		ops/ansible/group_vars/peering_forwarders/peering-forwarder.yml \
		"ops/federation/$FED_GB_ENVIRONMENT/peerings/$THEM/federation.toml" \
		"ops/ansible/group_vars/${FED_SITE//-/_}_hosts/peerings.yml"; do
		[ -f "$TREE/$f" ] || continue
		cmp -s "$TREE/$f" "$CFG/$f" 2>/dev/null && continue
		mkdir -p "$CFG/$(dirname "$f")"
		cp -f "$TREE/$f" "$CFG/$f"
		echo "  $f"
		n=$((n + 1))
	done
	echo "$n file(s) from goatbuilder"
}
# The forwarder's list of relayed peerings reads each carrier's public keys
# from files on this machine (<work>/pki). A missing file drops that peering
# from the list, so put every recorded key in place before goatbuilder runs.
seed_carrier_keys() {
	local d=$CFG/ops/federation/$FED_GB_ENVIRONMENT/carrier-keys f p k
	[ -d "$d" ] || return 0
	for f in "$d"/*.pub; do
		[ -f "$f" ] || continue
		p=$(basename "$f"); k=${p#*.}; p=${p%%.*}
		mkdir -p "$FED_STATE/gb/$p/pki"
		cmp -s "$f" "$FED_STATE/gb/$p/pki/$k" || cp -f "$f" "$FED_STATE/gb/$p/pki/$k"
	done
}
# Keep this peering's carrier public keys in the config once phase 2 has made
# them, so they survive this machine.
save_carrier_keys() {
	local d=$CFG/ops/federation/$FED_GB_ENVIRONMENT/carrier-keys k
	for k in edge-carrier.pub forwarder-carrier.pub; do
		[ -s "$GBW/pki/$k" ] || continue
		mkdir -p "$d"
		cmp -s "$GBW/pki/$k" "$d/$THEM.$k" 2>/dev/null || cp -f "$GBW/pki/$k" "$d/$THEM.$k"
	done
}
# The value after a marker in goatbuilder's output, e.g. "sha256:…".
gb_value() { grep -oE "$1[^ ,;\"]+" | head -1 | sed -E "s/^$1//"; }

# ---- what the page shows ---------------------------------------------------
# summary: the one line a finished step shows when it's collapsed.
summary() { show summary "$*"; }
# check: one line of a checklist. state is ok, warn or fail.
CHECK_N=0
CHECK_FAILED=0
check() {
	CHECK_N=$((CHECK_N + 1))
	[ "$1" = fail ] && CHECK_FAILED=$((CHECK_FAILED + 1))
	printf '::show check_%02d=%s|%s|%s\n' "$CHECK_N" "$1" "$2" "$3"
	printf '  [%s] %s: %s\n' "$1" "$2" "$3"
}
# groups: a code in groups of four with the group count first, as the guide
# has it read aloud ("11 groups: xlBt YGcA …").
groups() { local g; g=$(groups4 "$1"); echo "$(echo "$g" | wc -w) groups: $g"; }
# A failed step keeps what goatbuilder says about the code it stopped on, so
# the page can show the meaning and the fix beside the error.
rm -f "$W/${FED_PHASE:-step}.explain"
explain_from() {
	local code
	code=$(echo "$1" | grep -oE 'GB-[0-9]{3}' | grep -vx 'GB-192' | tail -1) || true
	[ -n "$code" ] || return 0
	{ echo "$code"; goatbuilder explain "$code" --color never 2>/dev/null || true; } >"$W/${FED_PHASE:-step}.explain"
}
# gb_run: goatbuilder federate, output kept in $GB_OUT and printed. Returns
# goatbuilder's exit code without stopping the script.
gb_run() {
	local rc=0
	GB_OUT=$(gb "$@" 2>&1) || rc=$?
	echo "$GB_OUT"
	return $rc
}
# gb_die: stop with a message, keeping goatbuilder's explanation of the code.
gb_die() { explain_from "${GB_OUT:-}"; die "$@"; }
