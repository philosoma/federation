package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GBWork is goatbuilder's work directory for a peering: its journal, the
// packages, the agreement, the plan and the acceptance evidence.
func (r *Runner) GBWork(them string) string { return filepath.Join(r.root, "gb", them) }

// PlanView is the bounded plan as a person reads it before approving.
type PlanView struct {
	Hash      string
	Verdict   string
	Transport string
	Subnet    string
	Conflicts []string
	Owns      []string // what the peering owns on shared hosts
	Restarts  []string
	NotReady  []string // readiness rows that didn't pass
	Admission string   // what our core will admit, as goatbuilder wrote it
}

// Plan reads <work>/plan.json and the admission review, if the plan step has
// written them.
func (r *Runner) Plan(them string) (*PlanView, bool) {
	b, err := os.ReadFile(filepath.Join(r.GBWork(them), "plan.json"))
	if err != nil {
		return nil, false
	}
	var raw struct {
		Hash      string `json:"hash"`
		Verdict   any    `json:"verdict"`
		Agreement struct {
			Transport  any `json:"transport"`
			LinkSubnet any `json:"link_subnet"`
		} `json:"agreement"`
		Conflicts []any `json:"conflicts"`
		Resources []struct {
			Kind, Name, On string
		} `json:"resources"`
		Restarts  []any `json:"restarts"`
		Readiness []struct {
			Check, Outcome, Code, Detail string
		} `json:"readiness"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return nil, false
	}
	v := &PlanView{Hash: raw.Hash, Verdict: text(raw.Verdict), Transport: text(raw.Agreement.Transport), Subnet: text(raw.Agreement.LinkSubnet)}
	for _, c := range raw.Conflicts {
		v.Conflicts = append(v.Conflicts, text(c))
	}
	for _, res := range raw.Resources {
		v.Owns = append(v.Owns, strings.TrimSpace(fmt.Sprintf("%s %s on %s", res.Kind, res.Name, res.On)))
	}
	for _, x := range raw.Restarts {
		v.Restarts = append(v.Restarts, text(x))
	}
	for _, x := range raw.Readiness {
		if o := strings.ToUpper(x.Outcome); o != "PASS" && o != "OK" && o != "" {
			v.NotReady = append(v.NotReady, strings.TrimSpace(fmt.Sprintf("%s: %s %s %s", x.Check, x.Outcome, x.Code, x.Detail)))
		}
	}
	if a, err := os.ReadFile(filepath.Join(r.GBWork(them), "admission", "review.md")); err == nil {
		v.Admission = strings.TrimSpace(string(a))
	} else if a, err := os.ReadFile(filepath.Join(r.GBWork(them), "admission-summary.txt")); err == nil {
		v.Admission = strings.TrimSpace(string(a))
	}
	return v, true
}

// text flattens a JSON value goatbuilder may write as a string or an object.
func text(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// Check is one acceptance check from the evidence.
type Check struct {
	Name, Verdict, Reason string
}

// AcceptanceView is our half of acceptance, from the evidence goatbuilder wrote.
type AcceptanceView struct {
	Verdict string
	Checks  []Check
	Counts  map[string]int
}

// Acceptance reads <work>/evidence/acceptance/result.json.
func (r *Runner) Acceptance(them string) (*AcceptanceView, bool) {
	b, err := os.ReadFile(filepath.Join(r.GBWork(them), "evidence", "acceptance", "result.json"))
	if err != nil {
		return nil, false
	}
	var raw struct {
		Verdict string         `json:"verdict"`
		Counts  map[string]int `json:"counts"`
		Checks  []struct {
			Name    string `json:"name"`
			Verdict string `json:"verdict"`
			Reason  string `json:"reason"`
		} `json:"checks"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return nil, false
	}
	v := &AcceptanceView{Verdict: raw.Verdict, Counts: raw.Counts}
	for _, c := range raw.Checks {
		v.Checks = append(v.Checks, Check{c.Name, strings.ToUpper(c.Verdict), c.Reason})
	}
	return v, true
}

// Explain is what `goatbuilder explain` said about the code a failed step
// stopped on, kept by the step beside its work.
func (r *Runner) Explain(them, phaseID string) string {
	b, err := os.ReadFile(filepath.Join(r.root, "work", them, phaseID+".explain"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
