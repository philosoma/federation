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
	res, ok := r.sends[them]
	return res, ok
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
	r.mu.Lock()
	if r.current != "" {
		busy := r.current
		r.mu.Unlock()
		return fmt.Errorf("something else is running (%s). Wait for it to finish.", busy)
	}
	r.current = them + "/" + SendPhase
	r.progress = Progress{Started: time.Now()}
	r.mu.Unlock()
	go r.runSend(them, name, topics, by)
	return nil
}

func (r *Runner) runSend(them, name string, topics []string, by string) {
	res := SendResult{Status: store.StatusFailed, By: by}
	defer func() {
		res.At = time.Now().UTC()
		r.mu.Lock()
		if r.sends == nil {
			r.sends = map[string]SendResult{}
		}
		r.sends[them] = res
		r.mu.Unlock()
		r.release()
		r.log.Info("release changed", "them", them, "topics", topics, "status", res.Status, "by", by)
		// Refresh the status clone so the page shows the new choice at once.
		r.measure(context.Background())
	}()

	work := filepath.Join(r.root, "work", them)
	logPath := r.LogPath(them, SendPhase)
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
	fmt.Fprintf(logf, "== What we send %s: %s\n== %s, by %s\n\n", them, strings.Join(topics, ", "), time.Now().UTC().Format(time.RFC3339), orDash(by))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(r.root, "scripts", "send.sh"))
	cmd.Dir = work
	cmd.Env = append(append([]string{}, r.env...),
		"FED_US="+r.us, "FED_THEM="+them, "FED_NAME="+name, "FED_WORK="+work,
		"FED_STATE="+r.root, "FED_SCRIPTS="+filepath.Join(r.root, "scripts"),
		"FED_SEND="+strings.Join(topics, " "))
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
