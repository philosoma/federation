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
	for _, ph := range runner.Phases {
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

func TestFlags(t *testing.T) {
	if countries["can"].Flag != "ca" || countries["usa"].Name != "United States" {
		t.Fatalf("country table: %+v %+v", countries["can"], countries["usa"])
	}
	if _, err := staticFS.ReadFile("static/flags/ca.svg"); err != nil {
		t.Fatal("ca.svg not bundled")
	}
	h, _, _ := newTest(t, nil)
	do(h, "POST", "/peerings", url.Values{"them": {"usa"}, "name": {"United States"}}, nil)
	do(h, "POST", "/peerings", url.Values{"them": {"uk1"}, "name": {"Test"}}, nil)
	body := do(h, "GET", "/", nil, nil).Body.String()
	if !strings.Contains(body, `src="/static/flags/us.svg"`) || !strings.Contains(body, `flag-code" title="No flag for this code">UK1<`) {
		t.Fatalf("flags on the status page:\n%s", body)
	}
}

func TestCrossSiteRefused(t *testing.T) {
	h, _, _ := newTest(t, nil)
	if w := do(h, "POST", "/peerings", url.Values{"them": {"can"}}, map[string]string{"Sec-Fetch-Site": "cross-site"}); w.Code != http.StatusForbidden {
		t.Fatalf("cross-site: %d", w.Code)
	}
	if w := do(h, "POST", "/peerings", url.Values{"them": {"can"}}, map[string]string{"Origin": "https://evil.example"}); w.Code != http.StatusForbidden {
		t.Fatalf("origin: %d", w.Code)
	}
	if w := do(h, "POST", "/peerings", url.Values{"them": {"can"}}, map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "null"}); w.Code != http.StatusSeeOther {
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

const testKey = "v56nMz4/abcdefghijklmnopqrstuvwxyzABCDEFGHI="

// run starts a step and waits for it.
func run(t *testing.T, h http.Handler, rn *runner.Runner, them, id string) {
	t.Helper()
	if w := do(h, "POST", "/run", url.Values{"n": {them}, "phase": {id}}, nil); strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatalf("run %s refused: %s", id, w.Header().Get("Location"))
	}
	wait(t, rn)
}

func record(t *testing.T, h http.Handler, them, id string, v url.Values) {
	t.Helper()
	v.Set("n", them)
	v.Set("phase", id)
	if w := do(h, "POST", "/record", v, nil); strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatalf("record %s: %s", id, w.Header().Get("Location"))
	}
}

func TestGuidedFlow(t *testing.T) {
	h, st, rn := newTest(t, nil)
	if w := do(h, "GET", "/", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Federation links") {
		t.Fatalf("front page should be Status: %d", w.Code)
	}
	if w := do(h, "GET", "/setup", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Start a new peering") {
		t.Fatalf("empty setup page: %d", w.Code)
	}
	if w := do(h, "POST", "/peerings", url.Values{"them": {"Canada!"}}, nil); !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatal("a bad code was accepted")
	}
	do(h, "POST", "/peerings", url.Values{"them": {"usa"}, "name": {"United States"}, "listen": {"them"}}, nil)
	body := do(h, "GET", "/setup?n=usa", nil, nil).Body.String()
	for _, want := range []string{"Check we&#39;re ready", "Reserve the link", "Review the plan", "Step 1 of 7", "is-current", "is-locked"} {
		if !strings.Contains(body, want) {
			t.Fatalf("setup page lacks %q", want)
		}
	}
	// Steps run in order.
	if w := do(h, "POST", "/run", url.Values{"n": {"usa"}, "phase": {"g1"}}, nil); !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatal("Reserve ran before the readiness check")
	}
	run(t, h, rn, "usa", "ready")
	run(t, h, rn, "usa", "g1")
	run(t, h, rn, "usa", "g2")
	// The exchange waits for the read-back; the next step can't run before it.
	if w := do(h, "POST", "/run", url.Values{"n": {"usa"}, "phase": {"g3"}}, nil); !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatal("Agree ran before the keys were confirmed")
	}
	if w := do(h, "POST", "/record", url.Values{"n": {"usa"}, "phase": {"g2"}, "their_key": {"short"}, "ours_confirmed": {"on"}, "theirs_confirmed": {"on"}}, nil); !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatal("a malformed key was recorded")
	}
	record(t, h, "usa", "g2", url.Values{"their_key": {testKey}, "ours_confirmed": {"on"}, "theirs_confirmed": {"on"}})
	run(t, h, rn, "usa", "g3")
	record(t, h, "usa", "g3", url.Values{"digest_matched": {"on"}})
	run(t, h, rn, "usa", "g4")
	if w := do(h, "POST", "/record", url.Values{"n": {"usa"}, "phase": {"g4"}, "plan_ok": {"on"}}, nil); !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatal("the plan was approved without the admission")
	}
	record(t, h, "usa", "g4", url.Values{"plan_ok": {"on"}, "admission_ok": {"on"}})
	run(t, h, rn, "usa", "g5")
	run(t, h, rn, "usa", "g6")
	record(t, h, "usa", "g6", url.Values{"theirs_passed": {"on"}})
	p, _ := st.Get("usa")
	for _, ph := range runner.Phases {
		if r := p.Runs[ph.ID]; r == nil || r.Status != store.StatusPassed {
			t.Fatalf("%s not passed: %+v", ph.ID, r)
		}
	}
	body = do(h, "GET", "/setup?n=usa", nil, nil).Body.String()
	if !strings.Contains(body, "United States is federated") || !strings.Contains(body, "Choose what they get") {
		t.Fatal("no finish card")
	}
	// With every peering finished, Setup opens on the new-peering form.
	if body := do(h, "GET", "/setup", nil, nil).Body.String(); strings.Contains(body, `class="steps"`) || !strings.Contains(body, "Start a new peering") {
		t.Fatal("Setup should open on the new-peering form")
	}
}

func TestEarlierMethodRestart(t *testing.T) {
	h, st, _ := newTest(t, nil)
	// A record from before goatbuilder: no method, old phase ids.
	if err := st.Create("est", "Estonia", true, ""); err != nil {
		t.Fatal(err)
	}
	body := do(h, "GET", "/setup?n=est", nil, nil).Body.String()
	if !strings.Contains(body, "Set up with the earlier method") || strings.Contains(body, `class="steps"`) {
		t.Fatal("an earlier-method peering should offer a restart, not steps")
	}
	if w := do(h, "POST", "/run", url.Values{"n": {"est"}, "phase": {"ready"}}, nil); !strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatal("a step ran on an earlier-method peering")
	}
	do(h, "POST", "/restart", url.Values{"n": {"est"}}, nil)
	if p, _ := st.Get("est"); p.Method != store.MethodGoatbuilder || len(p.Runs) != 0 {
		t.Fatalf("restart: %+v", p)
	}
	if body := do(h, "GET", "/setup?n=est", nil, nil).Body.String(); !strings.Contains(body, `class="steps"`) {
		t.Fatal("no steps after the restart")
	}
}

func TestStatusPage(t *testing.T) {
	h, _, _ := newTest(t, nil)
	do(h, "POST", "/peerings", url.Values{"them": {"usa"}, "name": {"United States"}, "listen": {"them"}}, nil)
	body := do(h, "GET", "/", nil, nil).Body.String()
	if !strings.Contains(body, "In setup: step 1 of 7") {
		t.Fatalf("status page:\n%s", body)
	}
}
