// Package store keeps each peering's settings and phase results in one JSON file.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"
)

// Phase status.
const (
	StatusIdle    = ""        // never run
	StatusRunning = "running" // a run is in progress
	StatusPassed  = "passed"
	StatusFailed  = "failed"
	StatusWaiting = "waiting" // ran, and needs something from people before it counts
)

// Run is the latest result of one phase.
type Run struct {
	Status   string            `json:"status,omitempty"`
	Started  time.Time         `json:"started,omitempty"`
	Finished time.Time         `json:"finished,omitempty"`
	Exit     int               `json:"exit,omitempty"`
	By       string            `json:"by,omitempty"`
	Note     string            `json:"note,omitempty"`    // one line for the page: why it failed or waits
	Outputs  map[string]string `json:"outputs,omitempty"` // values the script shows on the page
	Confirm  map[string]string `json:"confirm,omitempty"` // what people recorded after the run
}

// Peering is one federation with a host nation.
type Peering struct {
	Them     string `json:"them"`
	Name     string `json:"name"`
	WeListen bool   `json:"we_listen"`
	// Method is how the phases are driven: "goatbuilder" (goatbuilder federate,
	// 0.5.1 and later) or "" for the earlier scripts (goat federate), which
	// the peerings set up before it keep.
	Method  string          `json:"method,omitempty"`
	Created time.Time       `json:"created"`
	Runs    map[string]*Run `json:"runs,omitempty"`
}

// Run returns the phase's run record, creating it.
func (p *Peering) Run(id string) *Run {
	if p.Runs == nil {
		p.Runs = map[string]*Run{}
	}
	if p.Runs[id] == nil {
		p.Runs[id] = &Run{}
	}
	return p.Runs[id]
}

type file struct {
	Peerings []*Peering `json:"peerings"`
}

// Store is safe for concurrent use.
type Store struct {
	mu   sync.Mutex
	path string
	data file
}

// Open loads the file, or starts empty. A run left "running" by a restart is
// marked failed: its process is gone.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	for _, p := range s.data.Peerings {
		for _, r := range p.Runs {
			if r.Status == StatusRunning {
				r.Status, r.Note, r.Finished = StatusFailed, "Interrupted: Federation restarted during the run.", time.Now().UTC()
			}
		}
	}
	return s, nil
}

// List returns copies of every peering, newest first.
func (s *Store) List() []Peering {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Peering, 0, len(s.data.Peerings))
	for i := len(s.data.Peerings) - 1; i >= 0; i-- {
		out = append(out, clone(s.data.Peerings[i]))
	}
	return out
}

// Get returns a copy of one peering.
func (s *Store) Get(them string) (Peering, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.find(them); p != nil {
		return clone(p), true
	}
	return Peering{}, false
}

// The package spec's sovereign_id: 2-16 characters, lower-case a-z 0-9 and -.
// Usually an ISO alpha-3 code (can, usa); also nato, nato-jfcnp or uk1.
var sovereign = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,15}$`)

// ValidSovereign reports whether id is a valid sovereign_id.
func ValidSovereign(id string) bool { return sovereign.MatchString(id) }

// Create adds a peering.
// MethodGoatbuilder drives a peering with `goatbuilder federate`.
const MethodGoatbuilder = "goatbuilder"

func (s *Store) Create(them, name string, weListen bool, method string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ValidSovereign(them) {
		return fmt.Errorf("the code must be 2 to 16 characters, lower-case letters, digits and -, such as can or uk1")
	}
	if s.find(them) != nil {
		return fmt.Errorf("there is already a peering with %s", them)
	}
	s.data.Peerings = append(s.data.Peerings, &Peering{Them: them, Name: name, WeListen: weListen, Method: method, Created: time.Now().UTC()})
	if err := s.save(); err != nil {
		s.data.Peerings = s.data.Peerings[:len(s.data.Peerings)-1]
		return err
	}
	return nil
}

// Update changes a peering under the lock and saves.
func (s *Store) Update(them string, fn func(*Peering) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.find(them)
	if p == nil {
		return os.ErrNotExist
	}
	before := clone(p)
	if err := fn(p); err != nil {
		*p = before
		return err
	}
	if err := s.save(); err != nil {
		*p = before
		return err
	}
	return nil
}

// SetListener changes who listens. Allowed until phase 5 has passed (after
// that it's in the package the partner holds); the earlier phases' results
// are cleared, because they were run with the old setting.
func (s *Store) SetListener(them string, weListen bool) error {
	return s.Update(them, func(p *Peering) error {
		if r := p.Runs["g2"]; r != nil && r.Status == StatusPassed {
			return fmt.Errorf("the packages are exchanged: who listens is in the package %s already holds. Back the peering out and start again to change it", them)
		}
		for _, r := range p.Runs {
			if r.Status == StatusRunning {
				return fmt.Errorf("wait for the running phase to finish")
			}
		}
		if p.WeListen != weListen {
			p.WeListen = weListen
			p.Runs = nil
		}
		return nil
	})
}

// Delete removes a peering's record.
func (s *Store) Delete(them string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.data.Peerings, func(p *Peering) bool { return p.Them == them })
	if i < 0 {
		return os.ErrNotExist
	}
	prev := s.data.Peerings
	s.data.Peerings = slices.Delete(slices.Clone(prev), i, i+1)
	if err := s.save(); err != nil {
		s.data.Peerings = prev
		return err
	}
	return nil
}

func (s *Store) find(them string) *Peering {
	for _, p := range s.data.Peerings {
		if p.Them == them {
			return p
		}
	}
	return nil
}

// save writes atomically: a crash leaves the old file or the new one.
func (s *Store) save() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".federation-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

func clone(p *Peering) Peering {
	b, _ := json.Marshal(p)
	var c Peering
	_ = json.Unmarshal(b, &c)
	return c
}
