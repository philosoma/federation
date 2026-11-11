package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Endpoint is the address a partner dials when we listen.
type Endpoint struct {
	Address  string // e.g. fed.example.org:51824
	Reserved bool   // true: phase 1 has fixed it; false: what phase 1 will choose
}

// EndpointFor gives the peering's public address: the one phase 1 chose, or,
// before phase 1, the one it will choose from the config as it stands (the
// first free peering slot N, port 51820+2N). ok is false when it can't tell.
func (r *Runner) EndpointFor(them, publicName string) (Endpoint, bool) {
	if n, ok := r.allocatedIndex(them); ok {
		return Endpoint{Address: fmt.Sprintf("%s:%d", publicName, 51820+2*n), Reserved: true}, true
	}
	cfg := filepath.Join(r.root, "repos", "config-status")
	if _, err := os.Stat(filepath.Join(cfg, ".git")); err != nil {
		return Endpoint{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "python3", filepath.Join(r.root, "scripts", "fedcfg.py"), "next-index", "--repo", cfg, "--them", them).Output()
	if err != nil {
		return Endpoint{}, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return Endpoint{}, false
	}
	return Endpoint{Address: fmt.Sprintf("%s:%d", publicName, 51820+2*n)}, true
}

// allocatedIndex reads N from the peering's alloc.env, written by phase 1.
func (r *Runner) allocatedIndex(them string) (int, bool) {
	b, err := os.ReadFile(filepath.Join(r.root, "work", them, "alloc.env"))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "N="); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			return n, err == nil
		}
	}
	return 0, false
}
