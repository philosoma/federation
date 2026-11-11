package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/philosoma/federation/internal/store"
)

// ExitWaiting is the exit code a script uses to say "not yet": the run did
// what it could, and something outside it has to happen first (their package
// isn't on the directory yet, say). The phase shows as waiting, not failed.
const ExitWaiting = 75

// Timeout bounds one run.
const Timeout = 45 * time.Minute

// Runner runs one phase at a time for the whole node: every phase touches the
// same edge and core, so two at once could fight.
type Runner struct {
	st   *store.Store
	root string // state root: scripts/, work/<them>/, logs/<them>/
	us   string
	env  []string
	log  *slog.Logger

	mu       sync.Mutex
	current  string // "<them>/<phase>" while a run is in progress
	progress Progress

	status    statusCache
	measureMu sync.Mutex            // one status check at a time
	sends     map[string]SendResult // last "what we send" change per partner
}

// Progress is where the running phase has got to, from its "::progress N/M
// label" lines.
type Progress struct {
	N, M    int
	Label   string
	Started time.Time
}

// Pct is the share of steps finished, counting the current one as half done.
func (p Progress) Pct() int {
	if p.M <= 0 {
		return 0
	}
	pct := (2*p.N - 1) * 100 / (2 * p.M)
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

// ProgressOf returns the progress of a phase if it is the one running.
func (r *Runner) ProgressOf(them, phaseID string) (Progress, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != them+"/"+phaseID {
		return Progress{}, false
	}
	return r.progress, true
}

// New writes the embedded scripts under root and returns a runner.
func New(st *store.Store, root, us string, env []string, log *slog.Logger) (*Runner, error) {
	return NewWithScripts(st, root, us, env, log, scripts)
}

// NewWithScripts is New with another set of scripts, under scripts/: tests
// use it so a run doesn't touch real hosts.
func NewWithScripts(st *store.Store, root, us string, env []string, log *slog.Logger, src fs.FS) (*Runner, error) {
	dir := filepath.Join(root, "scripts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	err := fs.WalkDir(src, "scripts", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, filepath.Base(path)), b, 0o700)
	})
	if err != nil {
		return nil, err
	}
	return &Runner{st: st, root: root, us: us, env: env, log: log}, nil
}

// Busy names the run in progress, or "".
func (r *Runner) Busy() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

// CanRun says whether a phase may start: every earlier phase that isn't
// optional has passed.
func CanRun(p *store.Peering, phaseID string) (bool, string) {
	i, _ := Lookup(phaseID)
	if i < 0 {
		return false, "unknown step"
	}
	if p.Method != store.MethodGoatbuilder {
		return false, "This peering was set up with the earlier method. Start it again with goatbuilder to run steps here."
	}
	for _, ph := range Phases[:i] {
		if ph.Optional {
			continue
		}
		if run := p.Runs[ph.ID]; run == nil || run.Status != store.StatusPassed {
			return false, fmt.Sprintf("Finish \"%s\" first.", ph.Title)
		}
	}
	return true, ""
}

// Start begins a phase in the background.
func (r *Runner) Start(them, phaseID, by string) error {
	_, ph := Lookup(phaseID)
	if ph == nil {
		return errors.New("unknown phase")
	}
	p, ok := r.st.Get(them)
	if !ok {
		return os.ErrNotExist
	}
	if ok, why := CanRun(&p, phaseID); !ok {
		return errors.New(why)
	}

	r.mu.Lock()
	if r.current != "" {
		busy := r.current
		r.mu.Unlock()
		return fmt.Errorf("another phase is running (%s). Wait for it to finish.", busy)
	}
	r.current = them + "/" + phaseID
	r.progress = Progress{Started: time.Now()}
	r.mu.Unlock()

	err := r.st.Update(them, func(p *store.Peering) error {
		run := p.Run(phaseID)
		*run = store.Run{Status: store.StatusRunning, Started: time.Now().UTC(), By: by}
		return nil
	})
	if err != nil {
		r.release()
		return err
	}
	go r.run(p, ph, by)
	return nil
}

func (r *Runner) release() {
	r.mu.Lock()
	r.current = ""
	r.progress = Progress{}
	r.mu.Unlock()
}

// LogPath is where a phase's latest log is kept.
func (r *Runner) LogPath(them, phaseID string) string {
	return filepath.Join(r.root, "logs", them, phaseID+".log")
}

// Tail returns the last n bytes of a phase's log.
func (r *Runner) Tail(them, phaseID string, n int64) string {
	f, err := os.Open(r.LogPath(them, phaseID))
	if err != nil {
		return ""
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > n {
		_, _ = f.Seek(-n, io.SeekEnd)
		b, _ := io.ReadAll(f)
		if i := strings.IndexByte(string(b), '\n'); i >= 0 {
			b = b[i+1:]
		}
		return "…\n" + string(b)
	}
	b, _ := io.ReadAll(f)
	return string(b)
}

var (
	showLine     = regexp.MustCompile(`^::show ([a-z0-9_]+)=(.*)$`)
	progressLine = regexp.MustCompile(`^::progress (\d+)/(\d+) ?(.*)$`)
)

func (r *Runner) run(p store.Peering, ph *Phase, by string) {
	defer r.release()
	logPath := r.LogPath(p.Them, ph.ID)
	work := filepath.Join(r.root, "work", p.Them)
	outputs := map[string]string{}
	note := ""
	exit := -1

	finish := func(status string) {
		err := r.st.Update(p.Them, func(pp *store.Peering) error {
			run := pp.Run(ph.ID)
			run.Status, run.Finished, run.Exit, run.Note, run.Outputs = status, time.Now().UTC(), exit, note, outputs
			run.Confirm = nil
			return nil
		})
		if err != nil {
			r.log.Error("record run", "them", p.Them, "phase", ph.ID, "error", err)
		}
		r.log.Info("phase finished", "them", p.Them, "phase", ph.ID, "status", status, "exit", exit, "by", by)
	}

	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		note = "Could not create the log folder: " + err.Error()
		finish(store.StatusFailed)
		return
	}
	if err := os.MkdirAll(work, 0o700); err != nil {
		note = "Could not create the work folder: " + err.Error()
		finish(store.StatusFailed)
		return
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		note = "Could not open the log: " + err.Error()
		finish(store.StatusFailed)
		return
	}
	defer logf.Close()
	fmt.Fprintf(logf, "== Phase %s: %s\n== %s, started by %s\n\n", ph.Num, ph.Title, time.Now().UTC().Format(time.RFC3339), orDash(by))

	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(r.root, "scripts", ph.Script))
	cmd.Dir = work
	cmd.Env = append(append([]string{}, r.env...), r.peeringEnv(&p, work)...)
	cmd.Env = append(cmd.Env, "FED_PHASE="+ph.ID, "FED_OTHERS_EXCHANGING="+r.othersExchanging(p.Them))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if m := showLine.FindStringSubmatch(line); m != nil {
				outputs[m[1]] = m[2]
				continue
			}
			if m := progressLine.FindStringSubmatch(line); m != nil {
				n, _ := strconv.Atoi(m[1])
				total, _ := strconv.Atoi(m[2])
				r.mu.Lock()
				r.progress.N, r.progress.M, r.progress.Label = n, total, m[3]
				r.mu.Unlock()
				continue
			}
			if rest, ok := strings.CutPrefix(line, "::note "); ok {
				note = rest
			}
			fmt.Fprintln(logf, line)
		}
		_, _ = io.Copy(io.Discard, pr)
	}()

	err = cmd.Run()
	pw.Close()
	wg.Wait()

	switch {
	case ctx.Err() == context.DeadlineExceeded:
		exit, note = -1, fmt.Sprintf("Stopped after %s.", Timeout)
		fmt.Fprintf(logf, "\n== stopped: took longer than %s\n", Timeout)
		finish(store.StatusFailed)
	case err == nil:
		exit = 0
		fmt.Fprintf(logf, "\n== finished: passed\n")
		if len(ph.After) > 0 {
			if note == "" {
				note = "Ran. Now record what was checked with the other side."
			}
			finish(store.StatusWaiting)
		} else {
			finish(store.StatusPassed)
		}
	default:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		}
		if exit == ExitWaiting {
			fmt.Fprintf(logf, "\n== finished: waiting (exit %d)\n", exit)
			if note == "" {
				note = "Not ready yet. Run it again later."
			}
			finish(store.StatusWaiting)
			return
		}
		fmt.Fprintf(logf, "\n== finished: FAILED (exit %d)\n", exit)
		if note == "" {
			note = fmt.Sprintf("Failed with exit %d. Open the log.", exit)
		}
		finish(store.StatusFailed)
	}
}

// peeringEnv is what a script knows about the peering. Values are passed as
// environment, never pasted into the script, so nothing in them runs as code.
func (r *Runner) peeringEnv(p *store.Peering, work string) []string {
	listen := "false"
	if p.WeListen {
		listen = "true"
	}
	env := []string{
		"FED_US=" + r.us,
		"FED_THEM=" + p.Them,
		"FED_NAME=" + p.Name,
		"FED_WE_LISTEN=" + listen,
		"FED_WORK=" + work,
		"FED_STATE=" + r.root,
		"FED_SCRIPTS=" + filepath.Join(r.root, "scripts"),
	}
	env = append(env, "FED_METHOD="+p.Method)
	if run := p.Runs["g2"]; run != nil && run.Confirm != nil && run.Confirm["their_key"] != "" {
		env = append(env, "FED_THEIR_KEY="+run.Confirm["their_key"])
	}
	if run := p.Runs["g2"]; run != nil && run.Outputs["raw_their_ca"] != "" {
		env = append(env, "FED_THEIR_CA="+run.Outputs["raw_their_ca"])
	}
	if run := p.Runs["g3"]; run != nil && run.Status == store.StatusPassed {
		env = append(env, "FED_DIGEST="+run.Outputs["raw_digest"])
	}
	if run := p.Runs["g4"]; run != nil && run.Status == store.StatusPassed {
		env = append(env, "FED_PLAN_HASH="+run.Outputs["raw_plan_hash"], "FED_ADMISSION_DIGEST="+run.Outputs["raw_admission_digest"])
	}
	return env
}

var keyShape = regexp.MustCompile(`^[A-Za-z0-9+/]{43}=$`)

// Record saves what people checked after a run. All fields filled and every
// box ticked: the phase passes.
func (r *Runner) Record(them, phaseID string, values map[string]string) error {
	_, ph := Lookup(phaseID)
	if ph == nil || len(ph.After) == 0 {
		return errors.New("nothing to record for this phase")
	}
	clean := map[string]string{}
	for _, f := range ph.After {
		v := strings.TrimSpace(values[f.Key])
		switch f.Kind {
		case "check":
			if v != "on" {
				return fmt.Errorf("tick %q first", f.Label)
			}
		default:
			v = strings.Join(strings.Fields(v), "")
			if f.Key == "their_key" && !keyShape.MatchString(v) {
				return errors.New("their verifying key should be 44 characters of base64, ending in =")
			}
			if v == "" {
				return fmt.Errorf("fill in %q", f.Label)
			}
		}
		clean[f.Key] = v
	}
	return r.st.Update(them, func(p *store.Peering) error {
		run := p.Run(phaseID)
		if run.Status != store.StatusWaiting || run.Exit != 0 {
			return errors.New("run the phase first: there's nothing to confirm yet")
		}
		run.Confirm, run.Status, run.Note = clean, store.StatusPassed, ""
		return nil
	})
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// othersExchanging names our other peerings whose package exchange is open
// (exchanged, agreement not yet confirmed). The directory holds one package per
// party, so a second exchange would replace the first one's package.
func (r *Runner) othersExchanging(them string) string {
	var out []string
	for _, p := range r.st.List() {
		if p.Them == them || p.Method != store.MethodGoatbuilder {
			continue
		}
		g2, g3 := p.Runs["g2"], p.Runs["g3"]
		if g2 != nil && g2.Status != "" && (g3 == nil || g3.Status != store.StatusPassed) {
			out = append(out, p.Them)
		}
	}
	return strings.Join(out, " ")
}
