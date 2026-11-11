// Package runner runs each federation phase's script in the background and
// keeps its log, so the page can show progress and, on a failure, why.
package runner

import (
	"embed"

	"github.com/philosoma/federation/internal/store"
)

//go:embed scripts/*
var scripts embed.FS

// Field is something people record after a phase runs, before it counts as
// passed: a value read over the phone, or a tick that the read-back matched.
type Field struct {
	Key   string
	Label string
	Kind  string // "text" or "check"
	Hint  string
}

// Phase is one row on the page, as the one-pager groups them.
type Phase struct {
	ID         string
	Num        string
	Title      string
	Does       string // one sentence for people
	WorkedWhen string
	Script     string // file under scripts/
	Confirm    string // non-empty: a confirm dialog before running
	After      []Field
	Optional   bool
}

// Phases is the page, top to bottom.
var Phases = []Phase{
	{
		ID: "p1", Num: "1", Title: "Preflight",
		Does:       "Gets the config, the edge and this link's interface, ports and subnet ready, then checks every input, including the directory.",
		WorkedWhen: "prints READY",
		Script:     "p1-preflight.sh",
	},
	{
		ID: "p2", Num: "2–4", Title: "Prepare and sign our package",
		Does:       "Brings up the federation interface on the edge, then measures and signs our package and lints it.",
		WorkedWhen: "a signed package; lint passes",
		Script:     "p2-package.sh",
	},
	{
		ID: "p5", Num: "5", Title: "Exchange and confirm",
		Does:       "Publishes our package and CA to the directory and fetches theirs. Then confirm with them that each code is correct: ours as they see it, and theirs as we see it.",
		WorkedWhen: "no FAIL row; both sides confirm every code",
		Script:     "p5-exchange.sh",
		After: []Field{
			{Key: "ours_confirmed", Label: "They confirmed our verifying key and CA fingerprint are correct", Kind: "check"},
			{Key: "theirs_confirmed", Label: "We confirmed their verifying key and CA fingerprint with them", Kind: "check"},
		},
	},
	{
		ID: "p6", Num: "6–9", Title: "Agreement and certificates",
		Does:       "Composes one agreement and verifies it, mints the peering certificates and renders the configuration. Then confirm with them that both sides have the same digest.",
		WorkedWhen: "one digest, identical on both sides",
		Script:     "p6-agreement.sh",
		After: []Field{
			{Key: "digest_matched", Label: "They read the same agreement digest", Kind: "check"},
		},
	},
	{
		ID: "p10", Num: "10", Title: "Admit the edge on our core",
		Does:       "Adds the peering to our config, merges it into main and redeploys the core router.",
		WorkedWhen: "the core accepts the edge's derived name",
		Script:     "p10-admit.sh",
		Confirm:    "This changes production: the core router restarts and every fabric client drops for a few seconds. Run phase 10 now?",
	},
	{
		ID: "p11", Num: "11", Title: "Open the tunnel",
		Does:       "Brings up the transport the agreement chose.",
		WorkedWhen: "a handshake on both edges",
		Script:     "p11-tunnel.sh",
	},
	{
		ID: "p12", Num: "12", Title: "Edge router and republisher",
		Does:       "Deploys the edge router, then the republisher on our core, which lifts release/<lane>/… onto from/<us>/<lane>/….",
		WorkedWhen: "the edge reaches our core on TCP 7447",
		Script:     "p12-edge.sh",
		Confirm:    "This deploys the edge router and starts the republisher on the core. Run phase 12 now?",
	},
	{
		ID: "p13", Num: "13", Title: "SPIRE trust domains",
		Does:       "Federates the SPIRE trust domains, when both sides run it.",
		WorkedWhen: "done, or NOT DONE by design",
		Script:     "p13-spire.sh",
		Optional:   true,
	},
	{
		ID: "p14", Num: "14", Title: "Acceptance",
		Does:       "Starts our responder, then sends a probe to them and waits for their answer. Each side proves its own round trip; checks that need the other side's core show as blocked.",
		WorkedWhen: "our round trip returns, nothing fails, and theirs passed too",
		Script:     "p14-acceptance.sh",
		After: []Field{
			{Key: "theirs_passed", Label: "They confirmed their round trip passed too", Kind: "check"},
		},
	},
}

// GBPhases is the page for a peering driven by `goatbuilder federate`
// (goatbuilder 0.5.1): the same fourteen phases, run by one command that stops
// where a person has to confirm or approve. Values are passed as flags.
var GBPhases = []Phase{
	{
		ID: "g1", Num: "—", Title: "Allocate",
		Does:       "Declares the peering in goatbuilder.yaml, then lets goatbuilder choose its interface, ports and subnet and render its files, and commits them to the config.",
		WorkedWhen: "the peering's values are committed",
		Script:     "gb-allocate.sh",
	},
	{
		ID: "g2", Num: "1–5", Title: "Exchange and confirm",
		Does:       "Checks our side, measures and signs our package, and swaps packages with them through the directory. Then read the codes aloud with them.",
		WorkedWhen: "both sides read every code back correctly",
		Script:     "gb-exchange.sh",
		After: []Field{
			{Key: "their_key", Label: "Their verifying key, as they read it to you", Kind: "text", Hint: "44 characters ending in ="},
			{Key: "ours_confirmed", Label: "They confirmed our verifying key and CA fingerprint", Kind: "check"},
			{Key: "theirs_confirmed", Label: "Their CA fingerprint above matches what they read out", Kind: "check"},
		},
	},
	{
		ID: "g3", Num: "6–9", Title: "Agreement",
		Does:       "Composes and verifies the agreement. Then read its digest to each other.",
		WorkedWhen: "one digest, identical on both sides",
		Script:     "gb-agreement.sh",
		After: []Field{
			{Key: "digest_matched", Label: "They read the same agreement digest", Kind: "check"},
		},
	},
	{
		ID: "g4", Num: "plan", Title: "Plan and admission",
		Does:       "Mints and renders everything, writes the bounded plan and shows what our core will admit. Changes no host. Read both, then approve.",
		WorkedWhen: "a plan with no conflicts, and an admission of exactly two new names",
		Script:     "gb-plan.sh",
		After: []Field{
			{Key: "plan_ok", Label: "I read the plan: no conflicts, nothing touches another peering", Kind: "check"},
			{Key: "admission_ok", Label: "I approve what our core admits: only the edge and the republisher are new", Kind: "check"},
		},
	},
	{
		ID: "g5", Num: "10–13", Title: "Deploy",
		Does:       "Carries out the approved plan: admits the edge on our core (the router restarts), opens the tunnel, deploys the edge router and republisher.",
		WorkedWhen: "the edge dials our core; the republisher is linked",
		Script:     "gb-deploy.sh",
		Confirm:    "This changes production: our core router restarts and every fabric client drops for a few seconds. Deploy the approved plan now?",
	},
	{
		ID: "g6", Num: "14", Title: "Acceptance",
		Does:       "Runs our half of acceptance against our core and edge. A required check counts only on PASS; their half comes in through a shared session.",
		WorkedWhen: "PASS, or INCOMPLETE waiting only on their half",
		Script:     "gb-test.sh",
		After: []Field{
			{Key: "theirs_passed", Label: "They confirmed their acceptance passed too", Kind: "check"},
		},
	},
}

// PhasesFor is the page for a peering's method.
func PhasesFor(method string) []Phase {
	if method == store.MethodGoatbuilder {
		return GBPhases
	}
	return Phases
}

// Lookup finds a phase by id, in either list; the index is within its list.
func Lookup(id string) (int, *Phase) {
	for _, list := range [][]Phase{Phases, GBPhases} {
		for i := range list {
			if list[i].ID == id {
				return i, &list[i]
			}
		}
	}
	return -1, nil
}
