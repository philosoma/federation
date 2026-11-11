package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/philosoma/federation/internal/store"
)

// SendPhase is the log name and busy marker of a "what we send" change.
const SendPhase = "send"

// Topic is a group of internal topics released together: the turret, say.
type Topic struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Detail string   `json:"detail,omitempty"`
	Src    []string `json:"src"`
}

// ReleasePlan is the release file: the topics we can send and who gets which.
type ReleasePlan struct {
	Set    string              `json:"set"`
	Topics []Topic             `json:"topics"`
	Send   map[string][]string `json:"send"`
	Legacy bool                `json:"-"` // still the old shared src -> dst list
}

// SendResult is how the last change for a partner went.
type SendResult struct {
	Status string // passed, failed
	Note   string
	At     time.Time
	By     string
}

func releaseFile() string {
	if f := os.Getenv("FED_RELEASE_FILE"); f != "" {
		return f
	}
	return "ops/release-bridge/release-bridge.json"
}

// Release reads the release file from the config clone the status check keeps
// fresh. ok is false when there isn't one.
func (r *Runner) Release() (ReleasePlan, bool) {
	b, err := os.ReadFile(filepath.Join(r.root, "repos", "config-status", releaseFile()))
	if err != nil {
		return ReleasePlan{}, false
	}
	var raw struct {
		ReleasePlan
		Map []json.RawMessage `json:"map"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return ReleasePlan{}, false
	}
	p := raw.ReleasePlan
	p.Legacy = len(raw.Map) > 0
	return p, true
}

// LastSend is how the last change for a partner went, if there was one since
// the app started.
func (r *Runner) LastSend(them string) (SendResult, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, ok := r.sends[SendPhase+"/"+them]
	return res, ok
}

// BackoutPhase is the log name and busy marker of a goatbuilder backout.
const BackoutPhase = "backout"

// LastBackout is how the last backout (or its dry run) for a partner went.
func (r *Runner) LastBackout(them string) (SendResult, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, ok := r.sends[BackoutPhase+"/"+them]
	return res, ok
}

// StartBackout backs one goatbuilder peering out, or with dry only surveys
// what that would remove. In the background, waiting its turn like a phase.
func (r *Runner) StartBackout(them, name string, dry bool, by string) error {
	p, ok := r.st.Get(them)
	if !ok || p.Method != store.MethodGoatbuilder {
		return errors.New("backout is for peerings run by goatbuilder federate")
	}
	if err := r.claim(them, BackoutPhase); err != nil {
		return err
	}
	d := "0"
	if dry {
		d = "1"
	}
	go r.runAction(them, name, BackoutPhase, "gb-backout.sh", []string{"FED_BACKOUT_DRY=" + d}, by)
	return nil
}

// claim takes the one-at-a-time slot for an action.
func (r *Runner) claim(them, kind string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != "" {
		return fmt.Errorf("something else is running (%s). Wait for it to finish.", r.current)
	}
	r.current = them + "/" + kind
	r.progress = Progress{Started: time.Now()}
	return nil
}

// StartSend sets which topics a partner gets: commits the release file and
// restarts the bridge, in the background. It waits its turn like a phase.
func (r *Runner) StartSend(them, name string, topics []string, by string) error {
	if !store.ValidSovereign(them) {
		return errors.New("unknown partner")
	}
	plan, ok := r.Release()
	if !ok {
		return errors.New("there's no release file in the config yet")
	}
	if plan.Legacy {
		return errors.New("the release file still uses the old shared list: convert it first (see the README)")
	}
	var known []string
	for _, t := range plan.Topics {
		known = append(known, t.ID)
	}
	for _, t := range topics {
		if !slices.Contains(known, t) {
			return fmt.Errorf("unknown topic %q", t)
		}
	}
	if err := r.claim(them, SendPhase); err != nil {
		return err
	}
	go r.runAction(them, name, SendPhase, "send.sh", []string{"FED_SEND=" + strings.Join(topics, " ")}, by)
	return nil
}

// runAction runs one script that isn't a phase (a release change, a backout)
// and keeps how it went.
func (r *Runner) runAction(them, name, kind, script string, extra []string, by string) {
	res := SendResult{Status: store.StatusFailed, By: by}
	defer func() {
		res.At = time.Now().UTC()
		r.mu.Lock()
		if r.sends == nil {
			r.sends = map[string]SendResult{}
		}
		r.sends[kind+"/"+them] = res
		r.mu.Unlock()
		r.log.Info("action finished", "kind", kind, "them", them, "env", extra, "status", res.Status, "by", by)
		// Refresh the status clone before the page stops showing "Applying",
		// or it redraws the old choice from the stale clone.
		r.measure(context.Background())
		r.release()
	}()

	work := filepath.Join(r.root, "work", them)
	logPath := r.LogPath(them, kind)
	if err := errors.Join(os.MkdirAll(filepath.Dir(logPath), 0o700), os.MkdirAll(work, 0o700)); err != nil {
		res.Note = err.Error()
		return
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		res.Note = err.Error()
		return
	}
	defer logf.Close()
	fmt.Fprintf(logf, "== %s %s %v\n== %s, by %s\n\n", kind, them, extra, time.Now().UTC().Format(time.RFC3339), orDash(by))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(r.root, "scripts", script))
	cmd.Dir = work
	cmd.Env = append(append([]string{}, r.env...),
		"FED_US="+r.us, "FED_THEM="+them, "FED_NAME="+name, "FED_WORK="+work,
		"FED_STATE="+r.root, "FED_SCRIPTS="+filepath.Join(r.root, "scripts"))
	cmd.Env = append(cmd.Env, extra...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			line := sc.Text()
			if rest, ok := strings.CutPrefix(line, "::note "); ok {
				res.Note = rest
			}
			if strings.HasPrefix(line, "::") {
				continue
			}
			fmt.Fprintln(logf, line)
		}
		_, _ = io.Copy(io.Discard, pr)
	}()
	err = cmd.Run()
	pw.Close()
	<-done
	if err != nil {
		fmt.Fprintf(logf, "\n== FAILED: %v\n", err)
		if res.Note == "" {
			res.Note = "The change failed. Open the log."
		}
		return
	}
	fmt.Fprintf(logf, "\n== done\n")
	res.Status, res.Note = store.StatusPassed, ""
}

// GBStatus is what `goatbuilder federate status` recorded for one peering, as
// the status check last wrote it: its next step and last acceptance verdict.
type GBStatus struct {
	Next       string
	Acceptance string
	LastRun    string
}

// GoatbuilderStatus reads it; ok is false when goatbuilder knows nothing of them.
func (r *Runner) GoatbuilderStatus(them string) (GBStatus, bool) {
	b, err := os.ReadFile(filepath.Join(r.root, "gb-status.json"))
	if err != nil {
		return GBStatus{}, false
	}
	var raw struct {
		Peerings []struct {
			Peering string `json:"peering"`
			Next    any    `json:"next"`
			LastRun *struct {
				Stage   string `json:"stage"`
				Outcome string `json:"outcome"`
			} `json:"last_run"`
			Acceptance *struct {
				Verdict   string `json:"verdict"`
				Agreement string `json:"agreement"`
			} `json:"acceptance"`
		} `json:"peerings"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return GBStatus{}, false
	}
	for _, p := range raw.Peerings {
		if p.Peering != them {
			continue
		}
		var s GBStatus
		if p.Next != nil {
			s.Next = fmt.Sprint(p.Next)
		}
		if p.LastRun != nil {
			s.LastRun = strings.TrimSpace(p.LastRun.Stage + " " + p.LastRun.Outcome)
		}
		if p.Acceptance != nil && p.Acceptance.Verdict != "" {
			s.Acceptance = p.Acceptance.Verdict
			if p.Acceptance.Agreement == "stale" {
				s.Acceptance += " (stale: a different agreement)"
			}
		}
		return s, true
	}
	return GBStatus{}, false
}
