package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const secret = "ghs_SECRET1"

type fake struct {
	mu         sync.Mutex
	srv        *httptest.Server
	dispatch   map[string]any
	auths      []string
	headers    http.Header
	conclusion string
	sha        string
	status     []string // statuses returned by successive run polls
	polls      int
	apiErr     bool
	hook       func(n int) // called on each request with its count
	n          int
}

func newFake(t *testing.T) *fake {
	f := &fake{conclusion: "success", sha: "abc", status: []string{"queued", "in_progress", "completed"}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.n++
		if f.hook != nil {
			f.hook(f.n)
		}
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		f.headers = r.Header
		if f.apiErr {
			http.Error(w, "boom "+r.Header.Get("Authorization"), 500)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/o/n":
			fmt.Fprint(w, `{"default_branch":"trunk"}`)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/chuggy-proof.yml/dispatches"):
			json.NewDecoder(r.Body).Decode(&f.dispatch)
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/chuggy-proof.yml/runs"):
			in := f.dispatch["inputs"].(map[string]any)
			if r.URL.Query().Get("event") != "workflow_dispatch" || r.URL.Query().Get("branch") != f.dispatch["ref"] {
				fmt.Fprint(w, `{"workflow_runs":[]}`)
				return
			}
			fmt.Fprintf(w, `{"workflow_runs":[{"id":1,"name":"other"},{"id":7,"name":"proof %s","head_sha":%q,"status":"queued","html_url":"http://run/7"}]}`, in["request"], f.sha)
		case r.URL.Path == "/repos/o/n/actions/runs/7":
			st := f.status[min(f.polls, len(f.status)-1)]
			f.polls++
			c := ""
			if st == "completed" {
				c = f.conclusion
			}
			fmt.Fprintf(w, `{"id":7,"head_sha":%q,"status":%q,"conclusion":%q}`, f.sha, st, c)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func tokenFile(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "tok")
	os.WriteFile(p, []byte(secret+"\n"), 0o600)
	return p
}

func exec(t *testing.T, f *fake, env map[string]string, extra ...string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	args := append([]string{"-repo", "o/n", "-outcome", "success", "-api", f.srv.URL, "-interval", "1ms", "-timeout", "5s"}, extra...)
	code := run(args, func(k string) string { return env[k] }, &out, &errb)
	all := out.String() + errb.String()
	if strings.Contains(all, secret) {
		t.Fatalf("token in output: %s", all)
	}
	return code, all
}

func TestSuccess(t *testing.T) {
	f := newFake(t)
	code, out := exec(t, f, map[string]string{"GH_TOKEN_FILE": tokenFile(t), "CHUG_COMMIT": "abc", "CHUG_REF": "refs/heads/chuggy/x/1"})
	if code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	if strings.Count(out, "run: http://run/7") != 1 || !strings.Contains(out, "status: in_progress") {
		t.Fatalf("output: %s", out)
	}
	if f.headers.Get("Accept") != "application/vnd.github+json" || f.headers.Get("X-GitHub-Api-Version") != "2022-11-28" {
		t.Fatalf("headers %v", f.headers)
	}
	in := f.dispatch["inputs"].(map[string]any)
	if in["outcome"] != "success" || in["request"] == "" {
		t.Fatalf("inputs %v", in)
	}
}

func TestFailureConclusion(t *testing.T) {
	f := newFake(t)
	f.conclusion = "failure"
	if code, out := exec(t, f, map[string]string{"GH_TOKEN_FILE": tokenFile(t)}); code != 1 {
		t.Fatalf("code %d: %s", code, out)
	}
}

func TestCommitMismatch(t *testing.T) {
	f := newFake(t)
	code, out := exec(t, f, map[string]string{"GH_TOKEN_FILE": tokenFile(t), "CHUG_COMMIT": "def"})
	if code != 1 || !strings.Contains(out, "not def") {
		t.Fatalf("code %d: %s", code, out)
	}
}

func TestAPIError(t *testing.T) {
	f := newFake(t)
	f.apiErr = true
	code, out := exec(t, f, map[string]string{"GH_TOKEN_FILE": tokenFile(t)})
	if code != 2 || !strings.Contains(out, "HTTP 500") {
		t.Fatalf("code %d: %s", code, out)
	}
}

func TestMissingTokenFile(t *testing.T) {
	f := newFake(t)
	for _, env := range []map[string]string{{}, {"GH_TOKEN_FILE": filepath.Join(t.TempDir(), "none")}} {
		code, out := exec(t, f, env)
		if code != 2 || !strings.Contains(out, "GH_TOKEN_FILE") {
			t.Fatalf("code %d: %s", code, out)
		}
	}
}

func TestTimeout(t *testing.T) {
	f := newFake(t)
	f.status = []string{"in_progress"}
	code, out := exec(t, f, map[string]string{"GH_TOKEN_FILE": tokenFile(t)}, "-timeout", "50ms")
	if code != 2 || !strings.Contains(out, "timed out") {
		t.Fatalf("code %d: %s", code, out)
	}
}

func TestTokenReadAgain(t *testing.T) {
	f := newFake(t)
	p := tokenFile(t)
	f.hook = func(n int) {
		if n == 3 {
			os.WriteFile(p, []byte("ghs_SECRET2"), 0o600)
		}
	}
	// the hook swaps the file while request 3 is in flight; request 4 must send the new token
	secondSecret := "ghs_SECRET2"
	var out, errb bytes.Buffer
	code := run([]string{"-repo", "o/n", "-outcome", "success", "-api", f.srv.URL, "-interval", "1ms"},
		func(k string) string { return map[string]string{"GH_TOKEN_FILE": p}[k] }, &out, &errb)
	if code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if f.auths[0] != "Bearer "+secret || f.auths[2] != "Bearer "+secret || f.auths[3] != "Bearer "+secondSecret || f.auths[len(f.auths)-1] != "Bearer "+secondSecret {
		t.Fatalf("auths %v", f.auths)
	}
	if strings.Contains(out.String()+errb.String(), secondSecret) {
		t.Fatal("token in output")
	}
}

func TestRef(t *testing.T) {
	env := func(ref string) map[string]string {
		return map[string]string{"GH_TOKEN_FILE": tokenFile(t), "CHUG_REF": ref}
	}
	f := newFake(t)
	if code, out := exec(t, f, env("refs/heads/chuggy/stack/T/2")); code != 0 || f.dispatch["ref"] != "chuggy/stack/T/2" {
		t.Fatalf("code %d ref %v %s", code, f.dispatch["ref"], out)
	}
	f = newFake(t)
	if code, out := exec(t, f, env("")); code != 0 || f.dispatch["ref"] != "trunk" {
		t.Fatalf("code %d ref %v %s", code, f.dispatch["ref"], out)
	}
	for _, bad := range []string{"refs/tags/v1", "main", "refs/heads/"} {
		f = newFake(t)
		code, out := exec(t, f, env(bad))
		if code != 2 || f.dispatch != nil || !strings.Contains(out, "not a branch") {
			t.Fatalf("%s: code %d dispatched %v: %s", bad, code, f.dispatch, out)
		}
	}
}
