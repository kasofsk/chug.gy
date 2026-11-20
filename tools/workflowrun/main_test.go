package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const secret = "s3cret-token"

// fake stands in for GitHub.
type fake struct {
	mu         sync.Mutex
	conclusion string
	sha        string
	errOn      string // path substring answered with 500
	pending    int    // polls answered in_progress before completed
	dispatch   map[string]any
	auths      []string
	onRequest  func(n int)
	n          int
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if f.onRequest != nil {
		f.onRequest(f.n)
	}
	f.auths = append(f.auths, r.Header.Get("Authorization"))
	if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
		http.Error(w, "bad headers", 400)
		return
	}
	if f.errOn != "" && strings.Contains(r.URL.Path, f.errOn) {
		http.Error(w, "boom", 500)
		return
	}
	p := r.URL.Path
	switch {
	case r.Method == "GET" && p == "/repos/o/n":
		w.Write([]byte(`{"default_branch":"trunk"}`))
	case r.Method == "POST" && strings.HasSuffix(p, "/workflows/chuggy-proof.yml/dispatches"):
		json.NewDecoder(r.Body).Decode(&f.dispatch)
		w.WriteHeader(204)
	case r.Method == "GET" && strings.HasSuffix(p, "/workflows/chuggy-proof.yml/runs"):
		if f.dispatch == nil || r.URL.Query().Get("event") != "workflow_dispatch" {
			w.Write([]byte(`{"workflow_runs":[]}`))
			return
		}
		req := f.dispatch["inputs"].(map[string]any)["request"].(string)
		json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []any{
			map[string]any{"id": 1, "display_title": "chuggy-proof other", "status": "completed"},
			f.run("queued", req),
		}})
	case r.Method == "GET" && p == "/repos/o/n/actions/runs/7":
		st := "completed"
		if f.pending > 0 {
			f.pending--
			st = "in_progress"
		}
		json.NewEncoder(w).Encode(f.run(st, ""))
	default:
		http.NotFound(w, r)
	}
}

func (f *fake) run(status, req string) map[string]any {
	m := map[string]any{"id": 7, "display_title": "chuggy-proof " + req, "html_url": "https://example/run/7",
		"status": status, "head_sha": f.sha}
	if status == "completed" {
		m["conclusion"] = f.conclusion
	}
	return m
}

func setup(t *testing.T, f *fake) (config, string) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	tok := filepath.Join(t.TempDir(), "token")
	os.WriteFile(tok, []byte(secret+"\n"), 0o600)
	return config{repo: "o/n", outcome: "success", api: srv.URL, timeout: 5 * time.Second,
		interval: time.Millisecond, tokenEnv: tok}, tok
}

func do(t *testing.T, c config) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), c, &out, &errb)
	all := out.String() + errb.String()
	if strings.Contains(all, secret) {
		t.Fatalf("token leaked in output: %q", all)
	}
	return code, all
}

func TestSuccess(t *testing.T) {
	f := &fake{conclusion: "success", sha: "abc", pending: 2}
	c, _ := setup(t, f)
	c.commit = "abc"
	code, out := do(t, c)
	if code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	if strings.Count(out, "run: https://example/run/7") != 1 || !strings.Contains(out, "status: in_progress") {
		t.Fatalf("output: %s", out)
	}
	for _, a := range f.auths {
		if a != "Bearer "+secret {
			t.Fatalf("auth %q", a)
		}
	}
}

func TestFailureConclusion(t *testing.T) {
	f := &fake{conclusion: "failure", sha: "abc"}
	c, _ := setup(t, f)
	c.outcome = "failure"
	if code, out := do(t, c); code != 1 {
		t.Fatalf("code %d: %s", code, out)
	}
}

func TestCommitMismatch(t *testing.T) {
	f := &fake{conclusion: "success", sha: "abc"}
	c, _ := setup(t, f)
	c.commit = "def"
	if code, out := do(t, c); code != 1 || !strings.Contains(out, "CHUG_COMMIT") {
		t.Fatalf("code %d: %s", code, out)
	}
}

func TestAPIError(t *testing.T) {
	f := &fake{conclusion: "success", errOn: "/dispatches"}
	c, _ := setup(t, f)
	if code, out := do(t, c); code != 2 || !strings.Contains(out, "500") {
		t.Fatalf("code %d: %s", code, out)
	}
}

func TestMissingTokenFile(t *testing.T) {
	f := &fake{}
	c, _ := setup(t, f)
	c.tokenEnv = filepath.Join(t.TempDir(), "none")
	if code, out := do(t, c); code != 2 || !strings.Contains(out, "GH_TOKEN_FILE") {
		t.Fatalf("code %d: %s", code, out)
	}
	c.tokenEnv = ""
	if code, out := do(t, c); code != 2 || !strings.Contains(out, "GH_TOKEN_FILE") {
		t.Fatalf("unset: code %d: %s", code, out)
	}
}

func TestTimeout(t *testing.T) {
	f := &fake{conclusion: "success", pending: 1 << 30}
	c, _ := setup(t, f)
	c.timeout = 150 * time.Millisecond
	if code, out := do(t, c); code != 2 || !strings.Contains(out, "timed out") {
		t.Fatalf("code %d: %s", code, out)
	}
}

func TestTokenReadAgain(t *testing.T) {
	f := &fake{conclusion: "success", sha: "abc", pending: 2}
	c, tok := setup(t, f)
	f.onRequest = func(n int) {
		if n == 3 {
			os.WriteFile(tok, []byte("rotated\n"), 0o600)
		}
	}
	// n==3 is rewritten while handling; the request after it must carry the new one.
	if code, out := do(t, c); code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	last := f.auths[len(f.auths)-1]
	if f.auths[0] != "Bearer "+secret || last != "Bearer rotated" {
		t.Fatalf("auths %q", f.auths)
	}
}

func TestRef(t *testing.T) {
	f := &fake{conclusion: "success", sha: "abc"}
	c, _ := setup(t, f)
	c.ref = "refs/heads/chuggy/stack/1/2"
	if code, out := do(t, c); code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	if f.dispatch["ref"] != "chuggy/stack/1/2" {
		t.Fatalf("dispatch %v", f.dispatch)
	}
	if in := f.dispatch["inputs"].(map[string]any); in["outcome"] != "success" || in["request"] == "" {
		t.Fatalf("inputs %v", in)
	}

	f = &fake{conclusion: "success", sha: "abc"}
	c, _ = setup(t, f)
	if code, out := do(t, c); code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	if f.dispatch["ref"] != "trunk" {
		t.Fatalf("default branch: %v", f.dispatch)
	}

	f = &fake{}
	c, _ = setup(t, f)
	c.ref = "refs/tags/v1"
	code, out := do(t, c)
	if code == 0 || f.n != 0 || !strings.Contains(out, "not a branch") {
		t.Fatalf("code %d requests %d: %s", code, f.n, out)
	}
}
