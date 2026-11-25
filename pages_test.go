package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

var twoPages = []pageDef{{Name: "home", Path: "/"}, {Name: "alt", Path: "/alt"}}

func abTest(route string) testDef {
	return testDef{ID: "t1", Route: route, Arms: []arm{{"home", 1}, {"alt", 3}}}
}

// The site's files plus a second page, "alt", that exists only in tests.
func testFiles(t *testing.T) fs.FS {
	t.Helper()
	files, err := fs.Sub(site, "site")
	if err != nil {
		t.Fatal(err)
	}
	home, err := fs.ReadFile(files, "pages/home.html")
	if err != nil {
		t.Fatal(err)
	}
	alt := string(home)
	return overlay{files, map[string]string{"pages/alt.html": alt, "extra.css": "p{}"}}
}

type overlay struct {
	fs.FS
	extra map[string]string
}

func (o overlay) Open(name string) (fs.File, error) {
	if body, ok := o.extra[name]; ok {
		return fstest.MapFS{name: {Data: []byte(body)}}.Open(name)
	}
	return o.FS.Open(name)
}

func testWithTests(t *testing.T, pages []pageDef, tests []testDef) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := newServerWith(config{dataDir: dir, host: "chug.gy"}, testFiles(t), pages, tests)
	if err != nil {
		t.Fatal(err)
	}
	s.limit = newLimiter(1000, time.Hour)
	return s.handler(), dir
}

func TestRegistryRefusals(t *testing.T) {
	files := testFiles(t)
	arms := []arm{{"home", 1}, {"alt", 1}}
	cases := map[string]struct {
		pages []pageDef
		tests []testDef
	}{
		"unknown page":      {twoPages, []testDef{{ID: "a", Route: "/x", Arms: []arm{{"home", 1}, {"nope", 1}}}}},
		"repeated ID":       {twoPages, []testDef{{ID: "a", Route: "/x", Arms: arms}, {ID: "a", Route: "/y", Arms: arms}}},
		"one arm":           {twoPages, []testDef{{ID: "a", Route: "/x", Arms: []arm{{"home", 1}}}}},
		"zero weight":       {twoPages, []testDef{{ID: "a", Route: "/x", Arms: []arm{{"home", 1}, {"alt", 0}}}}},
		"negative weight":   {twoPages, []testDef{{ID: "a", Route: "/x", Arms: []arm{{"home", 1}, {"alt", -1}}}}},
		"two tests a route": {twoPages, []testDef{{ID: "a", Route: "/x", Arms: arms}, {ID: "b", Route: "/x", Arms: arms}}},
		"bad ID":            {twoPages, []testDef{{ID: "A_", Route: "/x", Arms: arms}}},
		"bad page name":     {[]pageDef{{Name: "Home"}}, nil},
		"same page path":    {[]pageDef{{Name: "home", Path: "/"}, {Name: "alt", Path: "/"}}, nil},
		"page on /docs":     {[]pageDef{{Name: "home", Path: "/docs"}}, nil},
		"page under /docs/": {[]pageDef{{Name: "home", Path: "/docs/x"}}, nil},
		"page on llms.txt":  {[]pageDef{{Name: "home", Path: "/llms.txt"}}, nil},
		"page on redirect":  {[]pageDef{{Name: "home", Path: "/linux"}}, nil},
		"page on fixed":     {[]pageDef{{Name: "home", Path: "/healthz"}}, nil},
		"route on /docs/":   {twoPages, []testDef{{ID: "a", Route: "/docs/", Arms: arms}}},
		"route on redirect": {twoPages, []testDef{{ID: "a", Route: "/github", Arms: arms}}},
		"route on fixed":    {twoPages, []testDef{{ID: "a", Route: "/site.css", Arms: arms}}},
		"twice registered":  {[]pageDef{{Name: "home"}, {Name: "home"}}, nil},
	}
	for name, c := range cases {
		if _, err := newRegistry(files, c.pages, c.tests); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if _, err := newServerWith(config{}, files, c.pages, c.tests); err == nil {
			t.Errorf("%s: the server started", name)
		}
	}
}

func TestDeclaredPagesAndTestsAreValid(t *testing.T) {
	if _, err := newRegistry(testFiles(t), pageDefs, testDefs); err != nil {
		t.Fatal(err)
	}
}

func TestDrawFollowsWeights(t *testing.T) {
	td := abTest("/")
	n, alt := 8000, 0
	for i := 0; i < n; i++ {
		if td.draw() == "alt" {
			alt++
		}
	}
	if f := float64(alt) / float64(n); f < 0.70 || f > 0.80 {
		t.Fatalf("alt drawn %.3f of the time, want about 0.75", f)
	}
}

func visit(h http.Handler, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", target, nil)
	r.Host = "chug.gy"
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestTestRouteAssignsAndSticks(t *testing.T) {
	h, _ := testWithTests(t, twoPages, []testDef{abTest("/")})
	w := visit(h, "/")
	if w.Code != 200 {
		t.Fatalf("answered %d", w.Code)
	}
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != "chug_test_t1" {
		t.Fatalf("cookies %v", cs)
	}
	c := cs[0]
	if (c.Value != "home" && c.Value != "alt") || c.Path != "/" || c.MaxAge != 90*24*3600 || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || !c.Secure {
		t.Fatalf("cookie %+v", c)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-test="t1"`) || !strings.Contains(body, `data-arm="`+c.Value+`"`) || !strings.Contains(body, `data-page="`+c.Value+`"`) {
		t.Fatal("the attributes are missing")
	}
	if !strings.Contains(body, `href="https://chug.gy/"`) {
		t.Fatal("the canonical link is not the route's")
	}
	if w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Vary") != "Cookie" {
		t.Fatalf("headers %v", w.Header())
	}
	for i := 0; i < 20; i++ {
		w2 := visit(h, "/", c)
		if len(w2.Result().Cookies()) != 0 || !strings.Contains(w2.Body.String(), `data-arm="`+c.Value+`"`) {
			t.Fatal("the assignment did not stick")
		}
	}
}

func TestStaleCookieReplacedAndOthersLeft(t *testing.T) {
	h, _ := testWithTests(t, twoPages, []testDef{abTest("/")})
	w := visit(h, "/", &http.Cookie{Name: "chug_test_t1", Value: "gone"}, &http.Cookie{Name: "chug_test_old", Value: "x"})
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != "chug_test_t1" || (cs[0].Value != "home" && cs[0].Value != "alt") {
		t.Fatalf("cookies %v", cs)
	}
}

func TestLoopbackCookieNotSecure(t *testing.T) {
	h, _ := testWithTests(t, twoPages, []testDef{abTest("/")})
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "localhost:8080"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Secure {
		t.Fatalf("cookies %+v", cs)
	}
}

func TestOwnPathOutsideTest(t *testing.T) {
	h, _ := testWithTests(t, twoPages, []testDef{abTest("/")})
	w := visit(h, "/alt")
	body := w.Body.String()
	if w.Code != 200 || len(w.Result().Cookies()) != 0 {
		t.Fatalf("answered %d with cookies", w.Code)
	}
	if strings.Contains(body, "data-test") || strings.Contains(body, "data-arm") {
		t.Fatal("outside a test the attributes must be absent")
	}
	if !strings.Contains(body, `data-page="alt"`) || !strings.Contains(body, `href="https://chug.gy/alt"`) {
		t.Fatal("page name or canonical wrong")
	}
	if w.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("cache %q", w.Header().Get("Cache-Control"))
	}
}

func TestUnknownURLIs404(t *testing.T) {
	h, _ := testWithTests(t, twoPages, []testDef{abTest("/")})
	for _, p := range []string{"/nope", "/alt/", "/alt/x"} {
		if w := visit(h, p); w.Code != 404 {
			t.Errorf("%s answered %d", p, w.Code)
		}
	}
}

func TestHomeWithoutTest(t *testing.T) {
	_, h, _ := testServer(t, false)
	w := visit(h, "/")
	body := w.Body.String()
	for _, want := range []string{`data-page="home"`, `<link rel="canonical" href="https://chug.gy/">`, `name="page" value="home"`, `name="test" value=""`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(body, "data-test") || strings.Contains(body, "data-arm") {
		t.Fatal("attributes outside a test")
	}
	if w.Header().Get("Cache-Control") != "public, max-age=300" || len(w.Result().Cookies()) != 0 {
		t.Fatal("home's caching or cookies changed")
	}
}

func TestPageStylesheet(t *testing.T) {
	pages := []pageDef{{Name: "home", Path: "/", Stylesheet: "extra.css"}, {Name: "alt"}}
	h, _ := testWithTests(t, pages, nil)
	w := visit(h, "/extra.css")
	if w.Code != 200 || strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("answered %d, cache %q", w.Code, w.Header().Get("Cache-Control"))
	}
	w = visit(h, "/extra.css?v="+mustRegistry(t, pages).pages["home"].key)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("answered %d", w.Code)
	}
	if _, err := newRegistry(testFiles(t), []pageDef{{Name: "home", Stylesheet: "site.css"}}, nil); err == nil {
		t.Fatal("a stylesheet took a fixed route")
	}
}

func lastRecorded(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "requests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func submit(h http.Handler, page, test string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	v := url.Values{"email": {"a@example.com"}, "interest": {"use"}, "page": {page}, "test": {test}}
	r := httptest.NewRequest("POST", "/request-access", strings.NewReader(v.Encode()))
	r.Host = "chug.gy"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestFormRecordsOrigin(t *testing.T) {
	h, dir := testWithTests(t, twoPages, []testDef{abTest("/")})
	arm := &http.Cookie{Name: "chug_test_t1", Value: "alt"}

	submit(h, "alt", "t1", arm)
	if m := lastRecorded(t, dir); m["page"] != "alt" || m["test"] != "t1" || m["arm"] != "alt" {
		t.Fatalf("arm submission: %v", m)
	}
	submit(h, "alt", "", arm) // the same page at its own path
	if m := lastRecorded(t, dir); m["page"] != "alt" || m["test"] != nil || m["arm"] != nil {
		t.Fatalf("own-path submission: %v", m)
	}
	submit(h, "forged", "t1", arm)
	if m := lastRecorded(t, dir); m["page"] != nil || m["test"] != nil || m["arm"] != nil {
		t.Fatalf("forged page: %v", m)
	}
	submit(h, "alt", "nope", arm)
	if m := lastRecorded(t, dir); m["page"] != "alt" || m["test"] != nil || m["arm"] != nil {
		t.Fatalf("forged test: %v", m)
	}
	submit(h, "alt", "t1", &http.Cookie{Name: "chug_test_t1", Value: "home"})
	if m := lastRecorded(t, dir); m["page"] != "alt" || m["test"] != nil || m["arm"] != nil {
		t.Fatalf("forged arm: %v", m)
	}
	submit(h, "alt", "t1")
	if m := lastRecorded(t, dir); m["test"] != nil {
		t.Fatalf("no cookie: %v", m)
	}
	if w := submit(h, "alt", "t1", arm); w.Code != http.StatusSeeOther {
		t.Fatalf("answered %d", w.Code)
	}
}

func mustRegistry(t *testing.T, pages []pageDef) *registry {
	t.Helper()
	reg, err := newRegistry(testFiles(t), pages, nil)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}
