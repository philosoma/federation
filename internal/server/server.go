// Package server draws the one Federation page and starts phase runs.
package server

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/philosoma/federation/internal/runner"
	"github.com/philosoma/federation/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Config is what the server needs from the config file.
type Config struct {
	SiteName       string
	Flag           string
	RequiredGroups []string // empty: no group check (loopback only, enforced by main)
	FrameAncestors []string // origins allowed to show the page in a frame, e.g. Signpost; empty: none
	FlashlightURL  string   // Flashlight's address, for "see their data" links; empty: no links
	Us             string   // our sovereign id
	PublicName     string   // the forwarder's name partners dial, e.g. fed.example.org
}

type server struct {
	cfg     Config
	st      *store.Store
	run     *runner.Runner
	version string
	log     *slog.Logger
	tmpl    *template.Template
}

// New builds the HTTP handler.
func New(cfg Config, st *store.Store, run *runner.Runner, version string, log *slog.Logger) (http.Handler, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"ago":   ago,
		"label": outputLabel,
		"upper": strings.ToUpper,
		// flag gives the flag file for an alpha-3 code, or "" when there's none
		// (a code that isn't a country, such as uk1).
		"flag": func(them string) string { return countries[them].Flag },
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &server{cfg: cfg, st: st, run: run, version: version, log: log, tmpl: tmpl}

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	app := http.NewServeMux()
	// Status is the front page; Setup runs the phases.
	app.HandleFunc("GET /{$}", s.handleStatus)
	app.HandleFunc("GET /setup", s.handlePage)
	app.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
	})
	app.HandleFunc("GET /api/state", s.handleState)
	app.HandleFunc("POST /peerings", s.handleCreate)
	app.HandleFunc("POST /run", s.handleRun)
	app.HandleFunc("POST /record", s.handleRecord)
	app.HandleFunc("POST /delete", s.handleDelete)
	app.HandleFunc("POST /listen", s.handleListen)
	app.HandleFunc("POST /send", s.handleSend)
	app.HandleFunc("POST /backout", s.handleBackout)
	app.HandleFunc("GET /log", s.handleLog)
	app.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	root.Handle("/", s.requireGroup(sameOrigin(app)))
	return securityHeaders(root, cfg.FrameAncestors), nil
}

// ----- Access -------------------------------------------------------------

// requireGroup is a second check behind the oauth2-proxy door. Only
// X-Forwarded-Groups is read: oauth2-proxy sets it from the session.
func (s *server) requireGroup(next http.Handler) http.Handler {
	if len(s.cfg.RequiredGroups) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, line := range r.Header.Values("X-Forwarded-Groups") {
			for _, g := range strings.Split(line, ",") {
				if slices.Contains(s.cfg.RequiredGroups, strings.TrimSpace(g)) {
					next.ServeHTTP(w, r)
					return
				}
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "You do not have access to Federation.", http.StatusForbidden)
	})
}

// sameOrigin refuses cross-site writes. Sec-Fetch-Site is set by the browser
// and can't be forged by a page; Origin is the fallback for older clients.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
				if site != "same-origin" && site != "none" {
					http.Error(w, "Cross-site request refused.", http.StatusForbidden)
					return
				}
			} else if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || u.Host != r.Host {
					http.Error(w, "Cross-site request refused.", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler, frameAncestors []string) http.Handler {
	ancestors := "'none'"
	if len(frameAncestors) > 0 {
		ancestors = strings.Join(frameAncestors, " ")
	}
	csp := "default-src 'none'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors " + ancestors
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-cache")
		next.ServeHTTP(w, r)
	})
}

func viewer(r *http.Request) string {
	if e := r.Header.Get("X-Forwarded-Email"); e != "" {
		return e
	}
	return r.Header.Get("X-Forwarded-User")
}

// ----- Page ---------------------------------------------------------------

type pageData struct {
	Tab                                string // "setup" or "status"
	PublicName                         string
	Site, Flag, Viewer, Version, Error string
	Peerings                           []store.Peering
	Sel                                *store.Peering
	Busy                               string
	Rows                               []row
	Done, Total                        int
	CanChangeListener                  bool
	Backout                            *backoutView
	GB                                 *runner.GBStatus
	Endpoint                           *runner.Endpoint
	Now                                time.Time
	Status                             *statusView
}

type backoutView struct {
	Running, Busy bool
	Last, Note    string
}

type row struct {
	runner.Phase
	Status, Chip, StatusText string
	Note                     string
	Outputs                  []kv // values that belong to neither side
	Ours, Theirs             []kv // codes to give them, and codes to check with them
	Partner                  string
	ShowAfter                bool
	Confirm                  []kv
	CanRun                   bool
	Why                      string
	Log                      string
	OpenLog                  bool
	Finished                 time.Time
	By                       string
	Progress                 *progressView
}

type progressView struct {
	N       int    `json:"n"`
	M       int    `json:"m"`
	Pct     int    `json:"pct"`
	Label   string `json:"label"`
	Elapsed string `json:"elapsed"`
}

func (s *server) progressOf(them, phaseID string) *progressView {
	p, ok := s.run.ProgressOf(them, phaseID)
	if !ok {
		return nil
	}
	return &progressView{N: p.N, M: p.M, Pct: p.Pct(), Label: p.Label, Elapsed: elapsed(time.Since(p.Started))}
}

func elapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
}

type kv struct{ Key, Value string }

func (s *server) handlePage(w http.ResponseWriter, r *http.Request) {
	list := s.st.List()
	d := pageData{Tab: "setup", PublicName: s.cfg.PublicName, Site: s.cfg.SiteName, Flag: s.cfg.Flag, Viewer: viewer(r), Version: s.version,
		Error: r.URL.Query().Get("error"), Peerings: list, Busy: s.run.Busy(), Now: time.Now()}
	// /setup?new shows only the new-peering form. With no peering named and
	// every peering finished, that's what Setup opens on too.
	want := r.URL.Query().Get("n")
	_, showNew := r.URL.Query()["new"]
	if want == "" && !showNew {
		showNew = true
		for i := range list {
			if _, done, total := s.rows(&list[i], ""); done < total {
				showNew = false
				break
			}
		}
	}
	for i := range list {
		if showNew && want == "" {
			break
		}
		if want == "" || list[i].Them == want {
			d.Sel = &list[i]
			break
		}
	}
	if d.Sel != nil {
		d.Rows, d.Done, d.Total = s.rows(d.Sel, d.Busy)
		if d.Sel.Method == store.MethodGoatbuilder {
			b := &backoutView{Running: d.Busy == d.Sel.Them+"/"+runner.BackoutPhase, Busy: d.Busy != ""}
			if res, ok := s.run.LastBackout(d.Sel.Them); ok {
				b.Last = fmt.Sprintf("%s %s by %s", map[bool]string{true: "Passed", false: "Failed"}[res.Status == store.StatusPassed], ago(time.Now(), res.At), res.By)
				b.Note = res.Note
			}
			d.Backout = b
			if g, ok := s.run.GoatbuilderStatus(d.Sel.Them); ok {
				d.GB = &g
			}
		}
		p5 := d.Sel.Runs["p5"]
		d.CanChangeListener = d.Busy == "" && (p5 == nil || p5.Status != store.StatusPassed)
		if d.Sel.WeListen && s.cfg.PublicName != "" {
			if ep, ok := s.run.EndpointFor(d.Sel.Them, s.cfg.PublicName); ok {
				d.Endpoint = &ep
			}
		}
	}
	s.render(w, "page.html", d)
}

func (s *server) rows(p *store.Peering, busy string) (rows []row, done, total int) {
	for _, ph := range runner.PhasesFor(p.Method) {
		run := p.Runs[ph.ID]
		if run == nil {
			run = &store.Run{}
		}
		rw := row{Phase: ph, Status: run.Status, Note: run.Note, Finished: run.Finished, By: run.By}
		rw.Chip, rw.StatusText = chip(run.Status)
		rw.Partner = p.Name
		for _, k := range sortedKeys(run.Outputs) {
			switch {
			case strings.HasPrefix(k, "our_"):
				rw.Ours = append(rw.Ours, kv{k, run.Outputs[k]})
			case strings.HasPrefix(k, "their_"):
				rw.Theirs = append(rw.Theirs, kv{k, run.Outputs[k]})
			default:
				rw.Outputs = append(rw.Outputs, kv{k, run.Outputs[k]})
			}
		}
		// The ticks only after a run that got everything to check: a "not yet"
		// (their package isn't published) has nothing to confirm.
		rw.ShowAfter = run.Status == store.StatusWaiting && run.Exit == 0 && len(ph.After) > 0
		for _, f := range ph.After {
			if v, ok := run.Confirm[f.Key]; ok {
				if f.Kind == "check" {
					v = "yes"
				}
				rw.Confirm = append(rw.Confirm, kv{f.Label, v})
			}
		}
		rw.CanRun, rw.Why = runner.CanRun(p, ph.ID)
		if busy != "" {
			rw.CanRun, rw.Why = false, "Another phase is running."
		}
		if run.Status != "" {
			rw.Log = s.run.Tail(p.Them, ph.ID, 48<<10)
		}
		rw.OpenLog = run.Status == store.StatusFailed
		if run.Status == store.StatusRunning {
			rw.Progress = s.progressOf(p.Them, ph.ID)
		}
		if !ph.Optional {
			total++
			if run.Status == store.StatusPassed {
				done++
			}
		}
		rows = append(rows, rw)
	}
	return rows, done, total
}

func chip(status string) (string, string) {
	switch status {
	case store.StatusRunning:
		return "info", "Running"
	case store.StatusPassed:
		return "ok", "Passed"
	case store.StatusFailed:
		return "err", "Failed"
	case store.StatusWaiting:
		return "warn", "Waiting for you"
	}
	return "", "Not run"
}

// HasFlag reports whether a flag file is bundled for an alpha-2 code.
func HasFlag(code string) bool {
	_, err := staticFS.ReadFile("static/flags/" + code + ".svg")
	return err == nil && code != "" && !strings.ContainsAny(code, "./")
}

// ----- Status ---------------------------------------------------------------

type statusView struct {
	Checked                  string
	Errors                   []string
	Links                    []linkRow
	Live, Attention, Setting int
}

type linkRow struct {
	Them, Name              string
	Chip, StateText         string
	Handshake, Traffic      string
	EdgeRouter, Republisher string
	Expires                 string
	DaysLeft                int
	Problems                []string
	FlashFrom, FlashOurs    string
	SetupLink               string
	Admitted                bool
	Send                    *sendView
}

// sendView is the "what we send" box on a partner's card.
type sendView struct {
	Them     string
	Name     string
	Topics   []topicBox
	Applying bool   // the change is being applied now
	Failed   string // the last change failed: why
	Done     string // the last change: when and by whom
	Busy     bool   // something else is running: the boxes can't be saved
	Summary  string // what goes now, for the closed box: "Sentry turret, Sat tracker"
	Open     bool   // start open: applying, or the last change failed
}

type topicBox struct {
	ID, Name, Detail string
	On               bool
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	list := s.st.List()
	d := pageData{Tab: "status", PublicName: s.cfg.PublicName, Site: s.cfg.SiteName, Flag: s.cfg.Flag, Viewer: viewer(r), Version: s.version,
		Error: r.URL.Query().Get("error"), Peerings: list, Busy: s.run.Busy(), Now: time.Now()}
	snap := s.run.Status()
	v := &statusView{Errors: snap.Errors}
	if !snap.Checked.IsZero() {
		v.Checked = ago(time.Now(), snap.Checked)
	}
	records := map[string]store.Peering{}
	for _, p := range list {
		records[p.Them] = p
	}
	plan, havePlan := s.run.Release()
	seen := map[string]bool{}
	for _, l := range snap.Links {
		seen[l.Them] = true
		row := s.linkRow(l, records)
		if havePlan && !plan.Legacy {
			row.Send = s.sendView(l.Them, row.Name, plan, d.Busy)
			if row.FlashOurs != "" {
				set := plan.Set
				if set == "" {
					set = "REL-ALL"
				}
				row.FlashOurs = strings.TrimRight(s.cfg.FlashlightURL, "/") + "/?key=" + url.QueryEscape("from/"+s.cfg.Us+"/"+set+"/"+l.Them+"/**") + "&since=15m"
			}
		}
		// Admitted on the core but still being set up here (phase 10 passed,
		// 11-14 to go): that's progress, not a fault, so not red.
		if p, ok := records[l.Them]; ok {
			if _, done, total := s.rows(&p, ""); done < total {
				row.Chip, row.StateText, row.Problems = "", fmt.Sprintf("In setup: %d of %d phases", done, total), nil
				v.Setting++
				v.Links = append(v.Links, row)
				continue
			}
		}
		switch row.Chip {
		case "ok":
			v.Live++
		default:
			v.Attention++
		}
		v.Links = append(v.Links, row)
	}
	// Peerings still being set up in this app, not yet admitted on the core.
	for _, p := range list {
		if seen[p.Them] {
			continue
		}
		_, done, total := s.rows(&p, "")
		v.Setting++
		v.Links = append(v.Links, linkRow{Them: p.Them, Name: p.Name, Chip: "", StateText: fmt.Sprintf("In setup: %d of %d phases", done, total), SetupLink: "/setup?n=" + p.Them})
	}
	d.Status = v
	s.render(w, "page.html", d)
}

func (s *server) linkRow(l runner.LinkStatus, records map[string]store.Peering) linkRow {
	row := linkRow{Them: l.Them, Admitted: true, EdgeRouter: orDash(l.EdgeRouter), Republisher: orDash(l.Republisher), Expires: l.Expires}
	if p, ok := records[l.Them]; ok {
		row.Name, row.SetupLink = p.Name, "/setup?n="+l.Them
	} else if c, ok := countries[l.Them]; ok {
		row.Name = c.Name
	} else {
		row.Name = strings.ToUpper(l.Them)
	}
	severity := 0 // 0 green, 1 amber, 2 red
	raise := func(n int, why string) {
		if n > severity {
			severity = n
		}
		row.Problems = append(row.Problems, why)
	}
	switch {
	case l.HandshakeAge == nil:
		row.Handshake = "never"
		raise(2, "The tunnel has never completed a handshake.")
	default:
		age := time.Duration(*l.HandshakeAge) * time.Second
		row.Handshake = elapsed(age) + " ago"
		if age > 10*time.Minute {
			raise(2, "No tunnel handshake for "+elapsed(age)+": the link is down.")
		} else if age > 5*time.Minute {
			raise(1, "The last tunnel handshake is "+elapsed(age)+" old.")
		}
	}
	row.Traffic = fmt.Sprintf("%s in · %s out", bytesHuman(l.RX), bytesHuman(l.TX))
	if !strings.HasPrefix(l.EdgeRouter, "Up") {
		raise(2, "The edge router goat-zenoh-edge-"+l.Them+" isn't running.")
	}
	if !strings.HasPrefix(l.Republisher, "Up") {
		raise(1, "The republisher isn't running: nothing of ours reaches them.")
	}
	if t, err := time.Parse("2006-01-02", l.Expires); err == nil {
		row.DaysLeft = int(time.Until(t).Hours() / 24)
		if row.DaysLeft < 0 {
			raise(2, "The agreement has expired.")
		} else if row.DaysLeft < 14 {
			raise(1, fmt.Sprintf("The agreement ends in %d days: renew it.", row.DaysLeft))
		}
	}
	row.Chip, row.StateText = []string{"ok", "warn", "err"}[severity], []string{"Federated", "Needs attention", "Down"}[severity]
	if s.cfg.FlashlightURL != "" {
		base := strings.TrimRight(s.cfg.FlashlightURL, "/")
		row.FlashFrom = base + "/?key=" + url.QueryEscape("from/"+l.Them+"/**") + "&since=15m"
		if s.cfg.Us != "" {
			row.FlashOurs = base + "/?key=" + url.QueryEscape("from/"+s.cfg.Us+"/**") + "&since=15m"
		}
	}
	return row
}

func (s *server) sendView(them, name string, plan runner.ReleasePlan, busy string) *sendView {
	v := &sendView{Them: them, Name: name, Applying: busy == them+"/"+runner.SendPhase}
	v.Busy = busy != "" && !v.Applying
	on := plan.Send[them]
	for _, t := range plan.Topics {
		v.Topics = append(v.Topics, topicBox{ID: t.ID, Name: t.Name, Detail: t.Detail, On: slices.Contains(on, t.ID)})
		if slices.Contains(on, t.ID) {
			if v.Summary != "" {
				v.Summary += ", "
			}
			v.Summary += t.Name
		}
	}
	if v.Summary == "" {
		v.Summary = "nothing"
	}
	if res, ok := s.run.LastSend(them); ok {
		if res.Status == store.StatusFailed {
			v.Failed = res.Note
		} else {
			v.Done = "Changed " + ago(time.Now(), res.At) + " by " + res.By
		}
	}
	v.Open = v.Applying || v.Failed != ""
	return v
}

func (s *server) handleSend(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form.", http.StatusBadRequest)
		return
	}
	them := r.PostFormValue("n")
	name := strings.ToUpper(them)
	if p, ok := s.st.Get(them); ok {
		name = p.Name
	} else if c, ok := countries[them]; ok {
		name = c.Name
	}
	// Only partners the core admits: the box isn't shown for any other.
	admitted := false
	for _, l := range s.run.Status().Links {
		admitted = admitted || l.Them == them
	}
	var err error
	if !admitted {
		err = errors.New("that partner isn't admitted on our core")
	} else {
		err = s.run.StartSend(them, name, r.PostForm["topic"], viewer(r))
	}
	q := url.Values{}
	if err != nil {
		q.Set("error", err.Error())
	} else {
		s.log.Info("release change started", "them", them, "topics", r.PostForm["topic"], "by", viewer(r))
	}
	to := "/"
	if len(q) > 0 {
		to += "?" + q.Encode()
	}
	http.Redirect(w, r, to+"#link-"+them, http.StatusSeeOther)
}

func bytesHuman(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func orDash(s string) string {
	if s == "" {
		return "not running"
	}
	return s
}

// handleState is what the page polls while a phase runs.
func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	p, ok := s.st.Get(r.URL.Query().Get("n"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	type phaseState struct {
		ID       string        `json:"id"`
		Status   string        `json:"status"`
		Log      string        `json:"log,omitempty"`
		Progress *progressView `json:"progress,omitempty"`
	}
	out := struct {
		Busy   string       `json:"busy"`
		Phases []phaseState `json:"phases"`
	}{Busy: s.run.Busy()}
	for _, ph := range runner.PhasesFor(p.Method) {
		st := phaseState{ID: ph.ID}
		if run := p.Runs[ph.ID]; run != nil {
			st.Status = run.Status
			if run.Status == store.StatusRunning {
				st.Log = s.run.Tail(p.Them, ph.ID, 48<<10)
				st.Progress = s.progressOf(p.Them, ph.ID)
			}
		}
		out.Phases = append(out.Phases, st)
	}
	writeJSON(w, out)
}

func (s *server) handleLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !store.ValidSovereign(q.Get("n")) {
		http.NotFound(w, r)
		return
	}
	if _, ph := runner.Lookup(q.Get("phase")); ph == nil && q.Get("phase") != runner.SendPhase && q.Get("phase") != runner.BackoutPhase {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(s.run.LogPath(q.Get("n"), q.Get("phase")))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", time.Time{}, f)
}

func (s *server) handleCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form.", http.StatusBadRequest)
		return
	}
	them := strings.ToLower(strings.TrimSpace(r.PostFormValue("them")))
	name := strings.TrimSpace(r.PostFormValue("name"))
	if len(name) > 80 {
		name = name[:80]
	}
	if name == "" {
		name = strings.ToUpper(them)
	}
	method := store.MethodGoatbuilder
	if r.PostFormValue("method") == "legacy" {
		method = ""
	}
	if err := s.st.Create(them, name, r.PostFormValue("listen") != "them", method); err != nil {
		back(w, r, "", err)
		return
	}
	s.log.Info("peering started", "them", them, "by", viewer(r))
	back(w, r, them, nil)
}

func (s *server) handleRun(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form.", http.StatusBadRequest)
		return
	}
	them, phase := r.PostFormValue("n"), r.PostFormValue("phase")
	err := s.run.Start(them, phase, viewer(r))
	if err == nil {
		s.log.Info("phase started", "them", them, "phase", phase, "by", viewer(r))
	}
	if wantsJSON(r) {
		if err != nil {
			w.WriteHeader(http.StatusConflict)
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"ok": "started"})
		return
	}
	back(w, r, them, err)
}

func (s *server) handleRecord(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form.", http.StatusBadRequest)
		return
	}
	them, phase := r.PostFormValue("n"), r.PostFormValue("phase")
	values := map[string]string{}
	for k := range r.PostForm {
		values[k] = r.PostFormValue(k)
	}
	err := s.run.Record(them, phase, values)
	if err == nil {
		s.log.Info("phase recorded", "them", them, "phase", phase, "by", viewer(r))
	}
	back(w, r, them, err)
}

func (s *server) handleListen(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form.", http.StatusBadRequest)
		return
	}
	them := r.PostFormValue("n")
	if s.run.Busy() != "" {
		back(w, r, them, errors.New("wait for the running phase to finish"))
		return
	}
	weListen := r.PostFormValue("listen") != "them"
	err := s.st.SetListener(them, weListen)
	if err == nil {
		s.log.Info("listener changed", "them", them, "we_listen", weListen, "by", viewer(r))
	}
	back(w, r, them, err)
}

// handleBackout surveys (dry) or backs out one goatbuilder peering. The real
// one needs the partner's code typed, as goatbuilder asks at a terminal.
func (s *server) handleBackout(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form.", http.StatusBadRequest)
		return
	}
	them := r.PostFormValue("n")
	dry := r.PostFormValue("dry") == "1"
	p, ok := s.st.Get(them)
	if !ok {
		back(w, r, "", errors.New("no such peering"))
		return
	}
	if !dry && r.PostFormValue("confirm") != them {
		back(w, r, them, fmt.Errorf("type %s to back it out", them))
		return
	}
	err := s.run.StartBackout(them, p.Name, dry, viewer(r))
	if err == nil {
		s.log.Info("backout started", "them", them, "dry", dry, "by", viewer(r))
	}
	back(w, r, them, err)
}

func (s *server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form.", http.StatusBadRequest)
		return
	}
	them := r.PostFormValue("n")
	if r.PostFormValue("confirm") != them {
		back(w, r, them, fmt.Errorf("type %s to confirm", them))
		return
	}
	if s.run.Busy() != "" {
		back(w, r, them, errors.New("wait for the running phase to finish"))
		return
	}
	if err := s.st.Delete(them); err != nil {
		back(w, r, them, err)
		return
	}
	s.log.Info("peering record deleted", "them", them, "by", viewer(r))
	back(w, r, "", nil)
}

// ----- Helpers ------------------------------------------------------------

func back(w http.ResponseWriter, r *http.Request, them string, err error) {
	q := url.Values{}
	if them != "" {
		q.Set("n", them)
	}
	if err != nil {
		q.Set("error", err.Error())
	}
	to := "/setup"
	if len(q) > 0 {
		to += "?" + q.Encode()
	}
	if ph := r.PostFormValue("phase"); ph != "" && err == nil {
		to += "#" + ph
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// outputLabel names the values scripts show on the page.
func outputLabel(k string) string {
	switch k {
	case "our_key", "their_key":
		return "Verifying key"
	case "our_ca", "their_ca":
		return "CA fingerprint"
	case "digest":
		return "Agreement digest (confirm it's the same on both sides)"
	case "expires":
		return "Agreement ends"
	case "link":
		return "Link"
	case "endpoint":
		return "Our endpoint"
	}
	s := strings.ReplaceAll(k, "_", " ")
	return strings.ToUpper(s[:1]) + s[1:]
}

func sortedKeys(m map[string]string) []string {
	order := []string{"our_key", "our_ca", "their_key", "their_ca", "digest", "expires"}
	var out []string
	for _, k := range order {
		if _, ok := m[k]; ok {
			out = append(out, k)
		}
	}
	var rest []string
	for k := range m {
		if !slices.Contains(order, k) {
			rest = append(rest, k)
		}
	}
	slices.Sort(rest)
	return append(out, rest...)
}

func ago(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return t.Local().Format("2 Jan 2006")
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("render", "template", name, "error", err)
		http.Error(w, "Could not render the page.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}
