package runner

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// LinkStatus is one admitted peering as status.sh measured it.
type LinkStatus struct {
	Them         string `json:"them"`
	Iface        string `json:"iface"`
	Digest       string `json:"digest"`
	Expires      string `json:"expires"`
	HandshakeAge *int   `json:"handshake_age"`
	RX           int64  `json:"rx"`
	TX           int64  `json:"tx"`
	EdgeRouter   string `json:"edge_router"`
	Republisher  string `json:"republisher"`
}

// StatusSnapshot is the latest measurement of every link.
type StatusSnapshot struct {
	Checked time.Time
	Links   []LinkStatus
	Errors  []string
}

type statusCache struct {
	mu   sync.Mutex
	snap StatusSnapshot
}

// Status returns the latest link measurement.
func (r *Runner) Status() StatusSnapshot {
	r.status.mu.Lock()
	defer r.status.mu.Unlock()
	return r.status.snap
}

// WatchStatus measures the links now and then every interval until ctx ends.
// It's read-only and may run beside a phase.
func (r *Runner) WatchStatus(ctx context.Context, interval time.Duration) {
	for {
		r.measure(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (r *Runner) measure(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(r.root, "scripts", "status.sh"))
	cmd.Env = append(append([]string{}, r.env...), "FED_STATE="+r.root, "FED_US="+r.us)
	out, err := cmd.Output()
	var raw struct {
		Checked int64        `json:"checked"`
		Links   []LinkStatus `json:"links"`
		Errors  []string     `json:"errors"`
	}
	snap := StatusSnapshot{Checked: time.Now()}
	if err != nil {
		snap.Errors = []string{"status check failed: " + err.Error()}
	} else if err := json.Unmarshal(out, &raw); err != nil {
		snap.Errors = []string{"status check printed something unreadable"}
	} else {
		snap.Links, snap.Errors = raw.Links, raw.Errors
	}
	r.status.mu.Lock()
	// A failed check keeps the last links, so the page doesn't go blank.
	if snap.Links == nil {
		snap.Links = r.status.snap.Links
	}
	r.status.snap = snap
	r.status.mu.Unlock()
	if err != nil {
		r.log.Warn("link status", "error", err)
	}
}
