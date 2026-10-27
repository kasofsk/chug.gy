package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T, withToken bool) (*server, http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := config{dataDir: dir, host: "chug.gy"}
	if withToken {
		tokenFile := filepath.Join(dir, "token")
		if err := os.WriteFile(tokenFile, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg.adminTokenFile = tokenFile
	}
	s, err := newServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s, s.handler(), dir
}

func postForm(h http.Handler, host string, values url.Values, accept string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/request-access", strings.NewReader(values.Encode()))
	r.Host = host
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func get(h http.Handler, host, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", target, nil)
	r.Host = host
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestIndexServesWithPolicy(t *testing.T) {
	_, h, _ := testServer(t, false)
	w := get(h, "chug.gy", "/")
	if w.Code != 200 {
		t.Fatalf("index answered %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/site.css?v="+stylesKey) {
		t.Error("the index does not name the stylesheet by its key")
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Error("no content security policy")
	}
	if strings.Contains(body, "{{") {
		t.Error("a template hole was left in the page")
	}
}

func TestOtherHostRedirects(t *testing.T) {
	_, h, _ := testServer(t, false)
	w := get(h, "www.chug.gy", "/docs?x=1")
	if w.Code != 301 || w.Header().Get("Location") != "https://chug.gy/docs?x=1" {
		t.Errorf("www answered %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := get(h, "10.42.0.7:8080", "/healthz"); w.Code != 200 {
		t.Errorf("the probe on the pod address answered %d", w.Code)
	}
	if w := get(h, "localhost:8080", "/"); w.Code != 200 {
		t.Errorf("loopback answered %d", w.Code)
	}
}

func TestShortPathsRedirect(t *testing.T) {
	_, h, _ := testServer(t, false)
	w := get(h, "chug.gy", "/docs")
	if w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), "https://github.com/kasofsk/chuggy-docs") {
		t.Errorf("/docs answered %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestRequestIsKept(t *testing.T) {
	_, h, dir := testServer(t, true)
	w := postForm(h, "chug.gy", url.Values{
		"email": {"Ada@Example.com"}, "name": {"Ada"}, "github": {"@ada"},
		"interest": {"use"}, "about": {"a repo of ours"},
	}, "")
	if w.Code != 303 || w.Header().Get("Location") != "/requested" {
		t.Fatalf("the form answered %d %s", w.Code, w.Header().Get("Location"))
	}
	kept, err := os.ReadFile(filepath.Join(dir, "requests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var req accessRequest
	if err := json.Unmarshal(kept, &req); err != nil {
		t.Fatalf("the line is not one request: %v", err)
	}
	if req.Email != "Ada@Example.com" || req.GitHub != "ada" || req.Interest != "use" || req.At.IsZero() {
		t.Errorf("kept %+v", req)
	}

	r := httptest.NewRequest("GET", "/admin/requests", nil)
	r.Host = "chug.gy"
	r.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"email":"Ada@Example.com"`) {
		t.Errorf("admin read answered %d %q", rec.Code, rec.Body.String())
	}
}

func TestJSONAnswer(t *testing.T) {
	_, h, _ := testServer(t, false)
	w := postForm(h, "chug.gy", url.Values{"email": {"ada@example.com"}, "interest": {"invest"}}, "application/json")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "received") {
		t.Errorf("answered %d %s", w.Code, w.Body.String())
	}
	w = postForm(h, "chug.gy", url.Values{"email": {"not an address"}, "interest": {"use"}}, "application/json")
	if w.Code != 400 || !strings.Contains(w.Body.String(), "email") {
		t.Errorf("a bad address answered %d %s", w.Code, w.Body.String())
	}
}

func TestRefusals(t *testing.T) {
	_, h, dir := testServer(t, false)
	cases := map[string]url.Values{
		"honeypot":    {"email": {"a@b.co"}, "interest": {"use"}, "website": {"http://spam"}},
		"no interest": {"email": {"a@b.co"}, "interest": {"all"}},
		"bad github":  {"email": {"a@b.co"}, "interest": {"use"}, "github": {"not a login"}},
		"long note":   {"email": {"a@b.co"}, "interest": {"use"}, "about": {strings.Repeat("x", maxAbout+1)}},
		"named email": {"email": {"Ada <a@b.co>"}, "interest": {"use"}},
	}
	for name, values := range cases {
		if w := postForm(h, "chug.gy", values, ""); w.Code != 400 {
			t.Errorf("%s answered %d", name, w.Code)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "requests.jsonl")); !os.IsNotExist(err) {
		t.Error("a refused request was kept")
	}
}

func TestAdminNeedsToken(t *testing.T) {
	_, h, _ := testServer(t, true)
	if w := get(h, "chug.gy", "/admin/requests"); w.Code != 401 {
		t.Errorf("no token answered %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/admin/requests", nil)
	r.Host = "chug.gy"
	r.Header.Set("Authorization", "Bearer wrong-wrong-wrong-wrong-wrong")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Errorf("a wrong token answered %d", w.Code)
	}
	_, h, _ = testServer(t, false)
	if w := get(h, "chug.gy", "/admin/requests"); w.Code != 404 {
		t.Errorf("with no token configured the route answered %d", w.Code)
	}
	s, err := newServer(config{dataDir: t.TempDir(), adminTokenFile: filepath.Join(t.TempDir(), "absent")})
	if err != nil {
		t.Fatal(err)
	}
	if w := get(s.handler(), "chug.gy", "/admin/requests"); w.Code != 404 {
		t.Errorf("with the token file absent the route answered %d", w.Code)
	}
}

func TestNoStoreRefusesPolitely(t *testing.T) {
	s, err := newServer(config{host: "chug.gy"})
	if err != nil {
		t.Fatal(err)
	}
	w := postForm(s.handler(), "chug.gy", url.Values{"email": {"a@b.co"}, "interest": {"use"}}, "")
	if w.Code != 503 {
		t.Errorf("answered %d", w.Code)
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(2, time.Minute)
	l.now = func() time.Time { return now }
	if !l.allow("a") || !l.allow("a") || l.allow("a") {
		t.Error("the third request in the window was allowed")
	}
	if !l.allow("b") {
		t.Error("another address was held to the first's count")
	}
	now = now.Add(2 * time.Minute)
	if !l.allow("a") {
		t.Error("the window did not pass")
	}
}
