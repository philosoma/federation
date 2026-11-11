// Package runner runs each step of a federation in the background and keeps
// its log, so the page can show progress and, on a failure, why.
package runner

import "embed"

//go:embed scripts/*
var scripts embed.FS

// Field is something a person records after a step runs, before it counts as
// done: a value read over the phone, or a tick that a read-back matched.
type Field struct {
	Key   string
	Label string
	Kind  string // "text" or "check"
	Hint  string
}

// Phase is one step on the Setup page. The steps follow the operator guide
// ("Federate two sovereign environments with goatbuilder"): goatbuilder
// federate does the work, and a person confirms at phases 5, 6 and 10.
type Phase struct {
	ID         string
	Num        string // the goatbuilder phases this step covers, for logs and errors
	Title      string
	Does       string // one or two sentences for people
	Needs      string // what the operator needs to hand before running it
	WorkedWhen string
	Script     string // file under scripts/
	Confirm    string // non-empty: a confirm dialog before running
	After      []Field
	AfterIntro string // what to do with the fields
	Tag        string // a short marker: "With them", "Restarts the core"
	Optional   bool
}

// Phases is the Setup page, top to bottom.
var Phases = []Phase{
	{
		ID: "ready", Num: "before", Title: "Check we're ready",
		Does:       "Checks everything the run depends on: the config, the edge and core hosts, the directory, our keys and what we offer. Changes nothing.",
		WorkedWhen: "every check passes",
		Script:     "gb-ready.sh",
	},
	{
		ID: "g1", Num: "allocate", Title: "Reserve the link",
		Does:       "Declares the peering, lets goatbuilder choose its interface, ports and addresses beside the other peerings, and commits them to the config.",
		WorkedWhen: "the link's values are committed",
		Script:     "gb-allocate.sh",
	},
	{
		ID: "g2", Num: "1–5", Title: "Exchange packages and read the keys",
		Does:       "Measures and signs our package, publishes it to the directory and fetches theirs. Then, on a call, each side reads its codes aloud and the other checks them.",
		Needs:      "They must have run their side first. Have them on a call for the read-back.",
		WorkedWhen: "every code is read back correctly, both ways",
		Script:     "gb-exchange.sh",
		Tag:        "With them",
		AfterIntro: "On the call: read our two codes to them, group count first. Then they read theirs: compare their CA fingerprint with the one shown, and type their verifying key as they read it. If anything differs, stop: don't record.",
		After: []Field{
			{Key: "their_key", Label: "Their verifying key, as they read it", Kind: "text", Hint: "44 characters, ending in ="},
			{Key: "ours_confirmed", Label: "They confirmed our verifying key and our CA fingerprint", Kind: "check"},
			{Key: "theirs_confirmed", Label: "Their CA fingerprint matches what they read out", Kind: "check"},
		},
	},
	{
		ID: "g3", Num: "6", Title: "Agree",
		Does:       "Composes the one agreement from both packages and checks it against the keys you confirmed. Then read its digest to each other.",
		Needs:      "Stay on the call: the digest is read aloud too.",
		WorkedWhen: "one digest, the same on both sides",
		Script:     "gb-agreement.sh",
		Tag:        "With them",
		AfterIntro: "Read the digest to each other, group count first. If it differs, don't tick: goatbuilder federate diff names the field.",
		After: []Field{
			{Key: "digest_matched", Label: "They read the same agreement digest", Kind: "check"},
		},
	},
	{
		ID: "g4", Num: "7–9 and the plan", Title: "Review the plan",
		Does:       "Mints the certificates and renders every file from the agreement, then writes the plan and what our core will admit. Changes no host.",
		WorkedWhen: "no conflicts, and exactly two new names on our core",
		Script:     "gb-plan.sh",
		AfterIntro: "Read the plan and the admission below. Approve only if nothing conflicts, nothing touches another peering, and the only new names are this partner's edge and republisher.",
		After: []Field{
			{Key: "plan_ok", Label: "I approve the plan: no conflicts, and it touches only this peering", Kind: "check"},
			{Key: "admission_ok", Label: "I approve what our core admits: only this partner's edge and republisher are new", Kind: "check"},
		},
	},
	{
		ID: "g5", Num: "10–13", Title: "Deploy",
		Does:       "Carries out the approved plan: admits the edge on our core, opens the tunnel, starts the edge router and the republisher.",
		Needs:      "Their side should deploy around the same time: the tunnel needs both ends.",
		WorkedWhen: "the edge reaches our core and the republisher is linked",
		Script:     "gb-deploy.sh",
		Confirm:    "This changes production: our core router restarts and every fabric client drops for a few seconds. Deploy the approved plan now?",
		Tag:        "Restarts the core",
	},
	{
		ID: "g6", Num: "14", Title: "Prove it",
		Does:       "Runs acceptance from our side: delivery, refusals, identity and that no other partner's data leaks. A check counts only when it passes.",
		Needs:      "Their side deployed, and their acceptance running too.",
		WorkedWhen: "PASS on our side, and theirs passes too",
		Script:     "gb-test.sh",
		Tag:        "With them",
		AfterIntro: "Our half is recorded. Confirm with them that their acceptance passed as well.",
		After: []Field{
			{Key: "theirs_passed", Label: "They confirmed their acceptance passed too", Kind: "check"},
		},
	},
}

// Lookup finds a step by id.
func Lookup(id string) (int, *Phase) {
	for i := range Phases {
		if Phases[i].ID == id {
			return i, &Phases[i]
		}
	}
	return -1, nil
}
