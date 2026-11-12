package runner

import (
	"os"
	"path/filepath"
	"regexp"
)

// Endpoint is the address a partner dials when we listen.
type Endpoint struct {
	Address  string // e.g. fed.example.org:51824
	Reserved bool   // always true: it's read from the committed allocation
}

var (
	endpointLine = regexp.MustCompile(`(?m)^endpoint\s*=\s*"([^"]+)"`)
	listenerLine = regexp.MustCompile(`(?m)^listener\s*=\s*true`)
)

// EndpointFor gives the address the partner dials, from the federation.toml
// goatbuilder rendered for the peering when its link was reserved. ok is false
// before that, or when they listen.
func (r *Runner) EndpointFor(them, _ string) (Endpoint, bool) {
	m, _ := filepath.Glob(filepath.Join(r.root, "repos", "config-status", "ops", "federation", "*", "peerings", them, "federation.toml"))
	for _, f := range m {
		b, err := os.ReadFile(f)
		if err != nil || !listenerLine.Match(b) {
			continue
		}
		if e := endpointLine.FindSubmatch(b); e != nil {
			return Endpoint{Address: string(e[1]), Reserved: true}, true
		}
	}
	return Endpoint{}, false
}
