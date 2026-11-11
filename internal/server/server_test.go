package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/philosoma/federation/internal/runner"
	"github.com/philosoma/federation/internal/store"
)

func TestFrameAncestors(t *testing.T) {
	h, _, _ := newTest(t, nil)
	if csp := do(h, "GET", "/healthz", nil, nil).Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("default: %s", csp)
	}
	st, _ := store.Open(filepath.Join(t.TempDir(), "f.json"))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rn, _ := runner.NewWithScripts(st, t.TempDir(), "gbr", nil, log, fstest.MapFS{"scripts/x.sh": &fstest.MapFile{Data: []byte("true")}})
	h2, _ := New(Config{SiteName: "x", FrameAncestors: []string{"https://signpost.example"}}, st, rn, "t", log)
	if csp := do(h2, "GET", "/healthz", nil, nil).Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors https://signpost.example") {
		t.Fatalf("signpost: %s", csp)
	}
}

func newTest(t *testing.T, groups []string) (http.Handler, *store.Store, *runner.Runner) {
	t.Helper()
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(dir, "federation.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Stand-in scripts: each prints a line and passes, without touching a host.
	fake := fstest.MapFS{}
	for _, ph := range append(append([]runner.Phase{}, runner.Phases...), runner.GBPhases...) {
		fake["scripts/"+ph.Script] = &fstest.MapFile{Data: []byte("echo \"$FED_THEM $FED_US\"\necho '::show digest=sha256: 0000'\necho '::show our_key=xlBt YGcA'\necho '::show their_key=v56n Mz4/'\n")}
	}
	rn, err := runner.NewWithScripts(st, dir, "gbr", []string{"PATH=/usr/bin:/bin"}, log, fake)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Config{SiteName: "Example", Flag: "gb", RequiredGroups: groups, PublicName: "fed.example.org"}, st, rn, "test", log)
	if err != nil {
		t.Fatal(err)
	}
	return h, st, rn
}

func do(h http.Handler, method, path string, form url.Values, hdr map[string]string) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func wait(t *testing.T, rn *runner.Runner) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for rn.Busy() != "" {
		if time.Now().After(deadline) {
			t.Fatal("run did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestFlow(t *testing.T) {
	h, st, rn := newTest(t, nil)
	if w := do(h, "GET", "/", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Federation links") {
		t.Fatalf("front page should be Status: %d", w.Code)
	}
	if w := do(h, "GET", "/status", nil, nil); w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/" {
		t.Fatalf("old status address: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := do(h, "GET", "/setup", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Start a new peering") {
		t.Fatalf("empty page: %d", w.Code)
	}
	if w := do(h, "POST", "/peerings", url.Values{"method": {"legacy"}, "them": {"can"}, "name": {"Canada"}, "listen": {"us"}}, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", w.Code)
	}
	if w := do(h, "POST", "/peerings", url.Values{"method": {"legacy"}, "them": {"Canada!"}}, nil); !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatal("bad code accepted")
	}
	page := do(h, "GET", "/setup?n=can", nil, nil).Body.String()
	if !strings.Contains(page, "Preflight") || !strings.Contains(page, "We listen") {
		t.Fatal("peering page incomplete")
	}

	// Phases run in order.
	if w := do(h, "POST", "/run", url.Values{"n": {"can"}, "phase": {"p2"}}, nil); !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatal("phase 2 ran before phase 1")
	}
	do(h, "POST", "/run", url.Values{"n": {"can"}, "phase": {"p1"}}, nil)
	wait(t, rn)
	p, _ := st.Get("can")
	if p.Runs["p1"].Status != store.StatusPassed {
		t.Fatalf("p1: %+v", p.Runs["p1"])
	}
	if log := rn.Tail("can", "p1", 4096); !strings.Contains(log, "finished: passed") {
		t.Fatalf("log: %q", log)
	}
	if w := do(h, "GET", "/log?n=can&phase=p1", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Phase 1") {
		t.Fatalf("full log: %d", w.Code)
	}

	// Phase 5 waits for people, then passes on a valid record.
	do(h, "POST", "/run", url.Values{"n": {"can"}, "phase": {"p2"}}, nil)
	wait(t, rn)
	do(h, "POST", "/run", url.Values{"n": {"can"}, "phase": {"p5"}}, nil)
	wait(t, rn)
	p, _ = st.Get("can")
	if p.Runs["p5"].Status != store.StatusWaiting {
		t.Fatalf("p5: %+v", p.Runs["p5"])
	}
	if w := do(h, "POST", "/record", url.Values{"n": {"can"}, "phase": {"p5"}, "ours_confirmed": {"on"}}, nil); !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatal("record with one tick accepted")
	}
	do(h, "POST", "/record", url.Values{"n": {"can"}, "phase": {"p5"}, "ours_confirmed": {"on"}, "theirs_confirmed": {"on"}}, nil)
	p, _ = st.Get("can")
	if p.Runs["p5"].Status != store.StatusPassed {
		t.Fatalf("p5 after record: %+v", p.Runs["p5"])
	}
	// The page shows our codes and theirs in separate boxes.
	page = do(h, "GET", "/setup?n=can", nil, nil).Body.String()
	if !strings.Contains(page, `class="side ours"`) || !strings.Contains(page, `class="side theirs"`) {
		t.Fatal("ours/theirs boxes missing")
	}

	if w := do(h, "GET", "/api/state?n=can", nil, nil); !strings.Contains(w.Body.String(), `"status":"passed"`) {
		t.Fatalf("state: %s", w.Body)
	}

	// Who listens is fixed once phase 5 has passed.
	if w := do(h, "POST", "/listen", url.Values{"n": {"can"}, "listen": {"them"}}, nil); !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatal("listener changed after phase 5")
	}

	do(h, "POST", "/delete", url.Values{"n": {"can"}, "confirm": {"no"}}, nil)
	if _, ok := st.Get("can"); !ok {
		t.Fatal("deleted without confirmation")
	}
	do(h, "POST", "/delete", url.Values{"n": {"can"}, "confirm": {"can"}}, nil)
	if _, ok := st.Get("can"); ok {
		t.Fatal("not deleted")
	}
}

func TestListenerChangeClearsRuns(t *testing.T) {
	h, st, rn := newTest(t, nil)
	do(h, "POST", "/peerings", url.Values{"method": {"legacy"}, "them": {"usa"}, "listen": {"us"}}, nil)
	do(h, "POST", "/run", url.Values{"n": {"usa"}, "phase": {"p1"}}, nil)
	wait(t, rn)
	do(h, "POST", "/listen", url.Values{"n": {"usa"}, "listen": {"them"}}, nil)
	p, _ := st.Get("usa")
	if p.WeListen || len(p.Runs) != 0 {
		t.Fatalf("after change: listen=%v runs=%v", p.WeListen, p.Runs)
	}
}

func TestStatusPage(t *testing.T) {
	h, _, _ := newTest(t, nil)
	do(h, "POST", "/peerings", url.Values{"method": {"legacy"}, "them": {"usa"}, "name": {"United States"}, "listen": {"them"}}, nil)
	w := do(h, "GET", "/", nil, nil)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "Federation links") || !strings.Contains(body, "In setup: 0 of 8 phases") {
		t.Fatalf("status page: %d\n%s", w.Code, body)
	}
}

func TestFlags(t *testing.T) {
	if countries["can"].Flag != "ca" || countries["usa"].Name != "United States" {
		t.Fatalf("country table: %+v %+v", countries["can"], countries["usa"])
	}
	if _, err := staticFS.ReadFile("static/flags/ca.svg"); err != nil {
		t.Fatal("ca.svg not bundled")
	}
	h, _, _ := newTest(t, nil)
	do(h, "POST", "/peerings", url.Values{"method": {"legacy"}, "them": {"usa"}, "name": {"United States"}}, nil)
	do(h, "POST", "/peerings", url.Values{"method": {"legacy"}, "them": {"uk1"}, "name": {"Test"}}, nil)
	body := do(h, "GET", "/", nil, nil).Body.String()
	if !strings.Contains(body, `src="/static/flags/us.svg"`) || !strings.Contains(body, `flag-code" title="No flag for this code">UK1<`) {
		t.Fatalf("flags on the status page:\n%s", body)
	}
}

func TestEndpointShown(t *testing.T) {
	h, _, rn := newTest(t, nil)
	do(h, "POST", "/peerings", url.Values{"method": {"legacy"}, "them": {"est"}, "name": {"Estonia"}, "listen": {"us"}}, nil)
	// No config clone in the test, so no guess: the page says phase 1 sets it.
	if body := do(h, "GET", "/setup?n=est", nil, nil).Body.String(); !strings.Contains(body, "the port is set in phase 1") {
		t.Fatal("no fallback line before phase 1")
	}
	// After phase 1 the allocation is what counts.
	dir := filepath.Join(rn.LogPath("est", "p1"), "..", "..", "..", "work", "est")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alloc.env"), []byte("N=2\nIFACE=wg-fed2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if body := do(h, "GET", "/setup?n=est", nil, nil).Body.String(); !strings.Contains(body, `<code class="endpoint">fed.example.org:51824</code>`) {
		t.Fatal("endpoint from alloc.env not shown")
	}
}

func TestSetupTitleFlag(t *testing.T) {
	h, _, _ := newTest(t, nil)
	do(h, "POST", "/peerings", url.Values{"method": {"legacy"}, "them": {"est"}, "name": {"Estonia"}}, nil)
	if body := do(h, "GET", "/setup?n=est", nil, nil).Body.String(); !strings.Contains(body, `class="flag flag-title" src="/static/flags/ee.svg"`) {
		t.Fatal("no flag in the Setup title")
	}
}

func TestNewPeeringPage(t *testing.T) {
	h, _, _ := newTest(t, nil)
	do(h, "POST", "/peerings", url.Values{"method": {"legacy"}, "them": {"est"}, "name": {"Estonia"}}, nil)
	body := do(h, "GET", "/setup?new", nil, nil).Body.String()
	if strings.Contains(body, "Agreement and certificates") || !strings.Contains(body, "Start a new peering") {
		t.Fatal("/setup?new should show only the new-peering form")
	}
	if !strings.Contains(do(h, "GET", "/setup?n=est", nil, nil).Body.String(), `href="/setup?new">+ New peering`) {
		t.Fatal("no New peering button on a peering")
	}
}

func TestCrossSiteRefused(t *testing.T) {
	h, _, _ := newTest(t, nil)
	if w := do(h, "POST", "/peerings", url.Values{"method": {"legacy"}, "them": {"can"}}, map[string]string{"Sec-Fetch-Site": "cross-site"}); w.Code != http.StatusForbidden {
		t.Fatalf("cross-site: %d", w.Code)
	}
	if w := do(h, "POST", "/peerings", url.Values{"method": {"legacy"}, "them": {"can"}}, map[string]string{"Origin": "https://evil.example"}); w.Code != http.StatusForbidden {
		t.Fatalf("origin: %d", w.Code)
	}
	if w := do(h, "POST", "/peerings", url.Values{"method": {"legacy"}, "them": {"can"}}, map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "null"}); w.Code != http.StatusSeeOther {
		t.Fatalf("same-origin form: %d", w.Code)
	}
}

func TestGroups(t *testing.T) {
	h, _, _ := newTest(t, []string{"PLATFORM-ADMIN"})
	if w := do(h, "GET", "/", nil, nil); w.Code != http.StatusForbidden {
		t.Fatalf("no group: %d", w.Code)
	}
	if w := do(h, "GET", "/", nil, map[string]string{"X-Forwarded-Groups": "x, PLATFORM-ADMIN"}); w.Code != 200 {
		t.Fatalf("with group: %d", w.Code)
	}
	if w := do(h, "GET", "/healthz", nil, nil); w.Code != 200 {
		t.Fatalf("healthz: %d", w.Code)
	}
}

func TestWhatWeSend(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, _ := store.Open(filepath.Join(dir, "federation.json"))
	fake := fstest.MapFS{
		"scripts/status.sh": &fstest.MapFile{Data: []byte(`echo '{"checked":1,"links":[{"them":"can","iface":"wg-fed0","edge_router":"Up 1 hour","republisher":"Up 1 hour"}],"errors":[]}'`)},
		"scripts/send.sh":   &fstest.MapFile{Data: []byte(`echo "$FED_THEM:$FED_SEND" >"$FED_STATE/sent"`)},
	}
	rn, err := runner.NewWithScripts(st, dir, "gbr", []string{"PATH=/usr/bin:/bin"}, log, fake)
	if err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join(dir, "repos", "config-status", "ops", "release-bridge")
	os.MkdirAll(rel, 0o700)
	os.WriteFile(filepath.Join(rel, "release-bridge.json"), []byte(`{"set":"REL-ALL","topics":[
		{"id":"turret","name":"Sentry turret","src":["a/turret/cots"]},
		{"id":"sattrack","name":"Sat tracker","src":["b/sattrack/cots"]}],
		"send":{"can":["turret"]}}`), 0o600)
	ctx, cancel := context.WithCancel(context.Background())
	go rn.WatchStatus(ctx, time.Hour)
	for rn.Status().Checked.IsZero() {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	h, _ := New(Config{SiteName: "x", Us: "gbr", FlashlightURL: "https://fl.example"}, st, rn, "t", log)

	body := do(h, "GET", "/", nil, nil).Body.String()
	for _, want := range []string{"What we send", `value="turret" checked`, `value="sattrack">`, url.QueryEscape("from/gbr/REL-ALL/can/**")} {
		if !strings.Contains(body, want) {
			t.Fatalf("status page lacks %q\n%s", want, body)
		}
	}
	if w := do(h, "POST", "/send", url.Values{"n": {"can"}, "topic": {"turret", "nope"}}, nil); !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatalf("unknown topic accepted: %s", w.Header().Get("Location"))
	}
	if w := do(h, "POST", "/send", url.Values{"n": {"est"}, "topic": {"turret"}}, nil); !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatalf("a partner that isn't admitted accepted: %s", w.Header().Get("Location"))
	}
	if w := do(h, "POST", "/send", url.Values{"n": {"can"}, "topic": {"turret", "sattrack"}}, nil); strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatalf("send refused: %s", w.Header().Get("Location"))
	}
	wait(t, rn)
	if b, _ := os.ReadFile(filepath.Join(dir, "sent")); strings.TrimSpace(string(b)) != "can:turret sattrack" {
		t.Fatalf("send.sh got %q", b)
	}
	if res, ok := rn.LastSend("can"); !ok || res.Status != "passed" {
		t.Fatalf("last send: %+v", res)
	}
}

func TestGoatbuilderFlow(t *testing.T) {
	h, st, rn := newTest(t, nil)
	do(h, "POST", "/peerings", url.Values{"them": {"usa"}, "name": {"United States"}, "listen": {"them"}}, nil)
	p, _ := st.Get("usa")
	if p.Method != store.MethodGoatbuilder {
		t.Fatalf("a new peering should use goatbuilder: %q", p.Method)
	}
	body := do(h, "GET", "/setup?n=usa", nil, nil).Body.String()
	for _, want := range []string{"Allocate", "Plan and admission", "Deploy"} {
		if !strings.Contains(body, want) {
			t.Fatalf("goatbuilder rows missing %q", want)
		}
	}
	if strings.Contains(body, "Admit the edge on our core") {
		t.Fatal("legacy rows shown for a goatbuilder peering")
	}
	// The rows run in order, and g2's codes are recorded before g3 can run.
	do(h, "POST", "/run", url.Values{"n": {"usa"}, "phase": {"g1"}}, nil)
	wait(t, rn)
	do(h, "POST", "/run", url.Values{"n": {"usa"}, "phase": {"g2"}}, nil)
	wait(t, rn)
	if w := do(h, "POST", "/run", url.Values{"n": {"usa"}, "phase": {"g3"}}, nil); !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatal("g3 ran before g2 was confirmed")
	}
	if w := do(h, "POST", "/record", url.Values{"n": {"usa"}, "phase": {"g2"}, "their_key": {"v56nMz4/abcdefghijklmnopqrstuvwxyzABCDEFGHI="}, "ours_confirmed": {"on"}, "theirs_confirmed": {"on"}}, nil); strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatalf("record g2: %s", w.Header().Get("Location"))
	}
	if w := do(h, "POST", "/run", url.Values{"n": {"usa"}, "phase": {"g3"}}, nil); strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatalf("g3 refused after g2: %s", w.Header().Get("Location"))
	}
	wait(t, rn)
}
