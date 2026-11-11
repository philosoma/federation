// Package runner runs each federation phase's script in the background and
// keeps its log, so the page can show progress and, on a failure, why.
package runner

import "embed"

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

// Lookup finds a phase by id.
func Lookup(id string) (int, *Phase) {
	for i := range Phases {
		if Phases[i].ID == id {
			return i, &Phases[i]
		}
	}
	return -1, nil
}
