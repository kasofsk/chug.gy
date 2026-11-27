package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

var twoPages = []pageDef{{Name: "home", Path: "/home"}, {Name: "alt", Path: "/alt"}}

func abTest() testDef {
	return testDef{ID: "t1", Arms: []arm{{"home", 1}, {"alt", 3}}}
}

// The slot that serves abTest at route.
func abSlot(route string) []slotDef { return []slotDef{{Route: route, Test: "t1"}} }

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

func testWithTests(t *testing.T, pages []pageDef, slots []slotDef, tests []testDef) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	keepTmpdir(t)
	s, err := newServerWith(config{dataDir: dir, host: "chug.gy"}, testFiles(t), pages, slots, tests)
	if err != nil {
		t.Fatal(err)
	}
	s.limit = newLimiter(1000, time.Hour)
	return s.handler(), dir
}

// Each case is valid but for the one fault it names, and the error says so.
func TestRegistryRefusals(t *testing.T) {
	files := testFiles(t)
	arms := []arm{{"home", 1}, {"alt", 1}}
	testA := []testDef{{ID: "a", Arms: arms}}
	slotA := []slotDef{{Route: "/x", Test: "a"}}
	styled := []pageDef{{Name: "home", Path: "/home", Stylesheet: "extra.css"}, {Name: "alt", Path: "/alt"}}
	cases := map[string]struct {
		pages []pageDef
		slots []slotDef
		tests []testDef
		want  string
	}{
		"unknown page":       {twoPages, slotA, []testDef{{ID: "a", Arms: []arm{{"home", 1}, {"nope", 1}}}}, `arm "nope" is not a registered page`},
		"repeated ID":        {twoPages, []slotDef{{Route: "/x", Test: "a"}, {Route: "/y", Test: "a"}}, []testDef{{ID: "a", Arms: arms}, {ID: "a", Arms: arms}}, `test "a" is declared twice`},
		"one arm":            {twoPages, slotA, []testDef{{ID: "a", Arms: []arm{{"home", 1}}}}, "at least two arms"},
		"zero weight":        {twoPages, slotA, []testDef{{ID: "a", Arms: []arm{{"home", 1}, {"alt", 0}}}}, "has weight 0"},
		"negative weight":    {twoPages, slotA, []testDef{{ID: "a", Arms: []arm{{"home", 1}, {"alt", -1}}}}, "has weight -1"},
		"two tests a route":  {twoPages, []slotDef{{Route: "/x", Test: "a"}, {Route: "/x", Test: "b"}}, []testDef{{ID: "a", Arms: arms}, {ID: "b", Arms: arms}}, `slot "/x" is declared twice`},
		"two pages a route":  {twoPages, []slotDef{{Route: "/x", Page: "home"}, {Route: "/x", Page: "alt"}}, nil, `slot "/x" is declared twice`},
		"bad ID":             {twoPages, []slotDef{{Route: "/x", Test: "A_"}}, []testDef{{ID: "A_", Arms: arms}}, "an ID is lowercase"},
		"bad page name":      {[]pageDef{{Name: "Home", Path: "/home"}}, nil, nil, "a name is lowercase"},
		"no page path":       {[]pageDef{{Name: "home"}}, nil, nil, `page "home" has no path`},
		"page on /":          {[]pageDef{{Name: "home", Path: "/"}}, nil, nil, `page "home": its own path cannot be "/"`},
		"same page path":     {[]pageDef{{Name: "home", Path: "/same"}, {Name: "alt", Path: "/same"}}, nil, nil, `"/same" collides with page home`},
		"page on /docs":      {[]pageDef{{Name: "home", Path: "/docs"}}, nil, nil, "collides with a fixed route"},
		"page under /docs/":  {[]pageDef{{Name: "home", Path: "/docs/x"}}, nil, nil, "collides with a fixed route"},
		"page on llms.txt":   {[]pageDef{{Name: "home", Path: "/llms.txt"}}, nil, nil, "collides with a fixed route"},
		"page on redirect":   {[]pageDef{{Name: "home", Path: "/linux"}}, nil, nil, "collides with a fixed route"},
		"page on fixed":      {[]pageDef{{Name: "home", Path: "/healthz"}}, nil, nil, "collides with a fixed route"},
		"twice registered":   {[]pageDef{{Name: "home", Path: "/home"}, {Name: "home", Path: "/other"}}, nil, nil, `page "home" is registered twice`},
		"route on /docs/":    {twoPages, []slotDef{{Route: "/docs/x", Test: "a"}}, testA, `slot "/docs/x": "/docs/x" collides with a fixed route`},
		"route on redirect":  {twoPages, []slotDef{{Route: "/github", Test: "a"}}, testA, `slot "/github": "/github" collides with a fixed route`},
		"route on fixed":     {twoPages, []slotDef{{Route: "/site.css", Test: "a"}}, testA, `slot "/site.css": "/site.css" collides with a fixed route`},
		"route not a path":   {twoPages, []slotDef{{Route: "x", Page: "home"}}, nil, `slot "x": the route is not a path`},
		"slot names neither": {twoPages, []slotDef{{Route: "/x"}}, nil, `slot "/x" names neither a page nor a test`},
		"slot names both":    {twoPages, []slotDef{{Route: "/x", Page: "home", Test: "a"}}, testA, `slot "/x" names both a page and a test`},
		"slot unknown page":  {twoPages, []slotDef{{Route: "/x", Page: "nope"}}, nil, `slot "/x": page "nope" is not a registered page`},
		"slot unknown test":  {twoPages, []slotDef{{Route: "/x", Test: "nope"}}, nil, `slot "/x": test "nope" is not a declared test`},
		"slot on own path":   {twoPages, []slotDef{{Route: "/alt", Page: "home"}}, nil, `slot "/alt": "/alt" collides with page alt`},
		"slot on stylesheet": {styled, []slotDef{{Route: "/extra.css", Page: "alt"}}, nil, `"/extra.css" collides with the stylesheet of page home`},
		"test with no slot":  {twoPages, nil, testA, `test "a" is served by no slot`},
		"test in two slots":  {twoPages, []slotDef{{Route: "/x", Test: "a"}, {Route: "/y", Test: "a"}}, testA, `test "a" is already served by slot "/x"`},
	}
	for name, c := range cases {
		if _, err := newRegistry(files, c.pages, c.slots, c.tests); err == nil {
			t.Errorf("%s: accepted", name)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: the error %q does not say %q", name, err, c.want)
		}
		if _, err := newServerWith(config{}, files, c.pages, c.slots, c.tests); err == nil {
			t.Errorf("%s: the server started", name)
		}
	}
}

func TestDeclaredPagesAndTestsAreValid(t *testing.T) {
	if _, err := newRegistry(testFiles(t), pageDefs, slotDefs, testDefs); err != nil {
		t.Fatal(err)
	}
}

func TestDrawFollowsWeights(t *testing.T) {
	td := abTest()
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
	h, _ := testWithTests(t, twoPages, abSlot("/"), []testDef{abTest()})
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
	h, _ := testWithTests(t, twoPages, abSlot("/"), []testDef{abTest()})
	w := visit(h, "/", &http.Cookie{Name: "chug_test_t1", Value: "gone"}, &http.Cookie{Name: "chug_test_old", Value: "x"})
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != "chug_test_t1" || (cs[0].Value != "home" && cs[0].Value != "alt") {
		t.Fatalf("cookies %v", cs)
	}
}

func TestLoopbackCookieNotSecure(t *testing.T) {
	h, _ := testWithTests(t, twoPages, abSlot("/"), []testDef{abTest()})
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "localhost:8080"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Secure {
		t.Fatalf("cookies %+v", cs)
	}
}

// A page's own path is outside any test, even when the page is an arm and the
// visitor holds the test's cookie naming it.
func TestOwnPathOutsideTest(t *testing.T) {
	h, _ := testWithTests(t, twoPages, abSlot("/"), []testDef{abTest()})
	for _, name := range []string{"home", "alt"} {
		assertOwnPath(t, h, "/"+name, name)
		assertOwnPath(t, h, "/"+name, name, &http.Cookie{Name: "chug_test_t1", Value: name})
	}
}

func assertOwnPath(t *testing.T, h http.Handler, path, name string, cookies ...*http.Cookie) {
	t.Helper()
	w := visit(h, path, cookies...)
	body := w.Body.String()
	if w.Code != 200 || len(w.Result().Cookies()) != 0 {
		t.Fatalf("%s answered %d with cookies %v", path, w.Code, w.Result().Cookies())
	}
	if strings.Contains(body, "data-test") || strings.Contains(body, "data-arm") || !strings.Contains(body, `name="test" value=""`) {
		t.Fatalf("%s: outside a test the attributes must be absent", path)
	}
	if !strings.Contains(body, `data-page="`+name+`"`) || !strings.Contains(body, `<link rel="canonical" href="https://chug.gy`+path+`">`) {
		t.Fatalf("%s: page name or canonical wrong", path)
	}
	if w.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("%s: cache %q", path, w.Header().Get("Cache-Control"))
	}
}

// With the declarations on main, each page has its own URL outside the test,
// and "/" still draws and keeps an arm of home-redesign.
func TestDeclaredOwnPaths(t *testing.T) {
	_, h, _ := testServer(t, false)
	for _, name := range []string{"home", "orchestration"} {
		assertOwnPath(t, h, "/"+name, name)
		assertOwnPath(t, h, "/"+name, name, &http.Cookie{Name: "chug_test_home-redesign", Value: name})
	}
	w := visit(h, "/")
	cs := w.Result().Cookies()
	if w.Code != 200 || len(cs) != 1 || cs[0].Name != "chug_test_home-redesign" {
		t.Fatalf("/ answered %d with cookies %v", w.Code, cs)
	}
	for i := 0; i < 5; i++ {
		w := visit(h, "/", cs[0])
		body := w.Body.String()
		if len(w.Result().Cookies()) != 0 || !strings.Contains(body, `data-test="home-redesign" data-arm="`+cs[0].Value+`"`) {
			t.Fatal("the assignment on / did not stick")
		}
	}
}

// A slot naming a page serves it at the slot's route, outside any test.
func TestSlotServesPage(t *testing.T) {
	h, _ := testWithTests(t, twoPages, []slotDef{{Route: "/x", Page: "alt"}}, nil)
	assertOwnPath(t, h, "/x", "alt")
	assertOwnPath(t, h, "/alt", "alt")
}

func TestUnknownURLIs404(t *testing.T) {
	h, _ := testWithTests(t, twoPages, abSlot("/"), []testDef{abTest()})
	for _, p := range []string{"/nope", "/alt/", "/alt/x"} {
		if w := visit(h, p); w.Code != 404 {
			t.Errorf("%s answered %d", p, w.Code)
		}
	}
}

func TestHomeWithoutTest(t *testing.T) {
	h, _ := testWithTests(t, pageDefs, []slotDef{{Route: "/", Page: "home"}}, nil)
	w := visit(h, "/")
	body := w.Body.String()
	for _, want := range []string{`data-page="home"`, `<link rel="canonical" href="https://chug.gy/">`, `name="page" value="home"`, `name="test" value=""`, "/site.css?v=" + stylesKey} {
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

var (
	scriptTag = regexp.MustCompile(`<script[^>]*>`)
	linkTag   = regexp.MustCompile(`<link[^>]*>`)
	srcValue  = regexp.MustCompile(`\ssrc="([^"]*)"`)
	hrefValue = regexp.MustCompile(`\shref="([^"]*)"`)
	cssURL    = regexp.MustCompile(`url\(([^)]*)\)`)
)

func onThisSite(target string) bool {
	return strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//")
}

// What the policy the server sends drops from a page without a word: a style
// or script written into it, and anything loaded from another site. It
// answers the faults and the paths on this site that the page loads.
func policyFaults(body string) (faults, loads []string) {
	if strings.Contains(body, "<style") {
		faults = append(faults, "a style element")
	}
	if strings.Contains(body, ` style="`) {
		faults = append(faults, "a style attribute")
	}
	for _, tag := range scriptTag.FindAllString(body, -1) {
		if !srcValue.MatchString(tag) {
			faults = append(faults, "a script written into the page")
		}
	}
	for _, m := range srcValue.FindAllStringSubmatch(body, -1) {
		if !onThisSite(m[1]) {
			faults = append(faults, "loads "+m[1])
			continue
		}
		loads = append(loads, m[1])
	}
	for _, tag := range linkTag.FindAllString(body, -1) {
		if strings.Contains(tag, `rel="canonical"`) {
			continue
		}
		href := hrefValue.FindStringSubmatch(tag)
		if href == nil || !onThisSite(href[1]) {
			faults = append(faults, "loads "+tag)
			continue
		}
		loads = append(loads, href[1])
	}
	return faults, loads
}

// Each arm of each declared test, as a visitor holding its cookie gets it:
// the page says which test and arm it is, carries the form that records them,
// keeps to the policy, and everything it loads is served.
func TestDeclaredArmsRender(t *testing.T) {
	_, h, _ := testServer(t, false)
	reg := mustRegistry(t, pageDefs, slotDefs, testDefs)
	if len(testDefs) == 0 {
		t.Skip("no test is declared")
	}
	for _, td := range testDefs {
		route := reg.route[td.ID]
		for _, a := range td.Arms {
			w := visit(h, route, &http.Cookie{Name: cookieName(td.ID), Value: a.Page})
			if w.Code != 200 || len(w.Result().Cookies()) != 0 {
				t.Fatalf("%s/%s answered %d with cookies %v", td.ID, a.Page, w.Code, w.Result().Cookies())
			}
			body := w.Body.String()
			sheet := "/site.css?v=" + stylesKey
			if p := reg.pages[a.Page]; p.def.Stylesheet != "" {
				sheet = "/" + p.def.Stylesheet + "?v=" + p.key
			}
			for _, want := range []string{
				`data-page="` + a.Page + `"`,
				`data-test="` + td.ID + `" data-arm="` + a.Page + `"`,
				`<link rel="canonical" href="https://chug.gy` + route + `">`,
				`<link rel="stylesheet" href="` + sheet + `">`,
				`src="/site.js"`, `src="/analytics.js"`,
				`request-form" method="post" action="/request-access"`,
				`name="page" value="` + a.Page + `"`,
				`name="test" value="` + td.ID + `"`,
				`name="email"`, `name="interest" value="use"`, `name="website"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("%s/%s: missing %s", td.ID, a.Page, want)
				}
			}
			if strings.Contains(body, "{{") {
				t.Errorf("%s/%s: a template hole was left in the page", td.ID, a.Page)
			}
			faults, loads := policyFaults(body)
			for _, f := range faults {
				t.Errorf("%s/%s: the policy would drop %s", td.ID, a.Page, f)
			}
			for _, target := range loads {
				got := visit(h, target)
				if got.Code != 200 {
					t.Errorf("%s/%s loads %s, which answered %d", td.ID, a.Page, target, got.Code)
					continue
				}
				if strings.HasPrefix(target, "/img/") && got.Header().Get("Content-Type") != "image/webp" {
					t.Errorf("%s is served as %q", target, got.Header().Get("Content-Type"))
				}
				if !strings.Contains(target, ".css") {
					continue
				}
				for _, m := range cssURL.FindAllStringSubmatch(got.Body.String(), -1) {
					if !onThisSite(m[1]) {
						t.Errorf("%s: the policy would drop %s", target, m[0])
					} else if got := visit(h, m[1]); got.Code != 200 {
						t.Errorf("%s loads %s, which answered %d", target, m[1], got.Code)
					}
				}
			}
		}
	}
}

func TestPageStylesheet(t *testing.T) {
	pages := []pageDef{{Name: "home", Path: "/home", Stylesheet: "extra.css"}, {Name: "alt", Path: "/alt"}}
	h, _ := testWithTests(t, pages, nil, nil)
	w := visit(h, "/extra.css")
	if w.Code != 200 || strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("answered %d, cache %q", w.Code, w.Header().Get("Cache-Control"))
	}
	w = visit(h, "/extra.css?v="+mustRegistry(t, pages, nil, nil).pages["home"].key)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("answered %d", w.Code)
	}
	if _, err := newRegistry(testFiles(t), []pageDef{{Name: "home", Path: "/home", Stylesheet: "site.css"}}, nil, nil); err == nil {
		t.Fatal("a stylesheet took a fixed route")
	} else if !strings.Contains(err.Error(), `the stylesheet of page home: "/site.css" collides with a fixed route`) {
		t.Fatalf("the error %q does not name the stylesheet's fixed route", err)
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
	h, dir := testWithTests(t, twoPages, abSlot("/"), []testDef{abTest()})
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

func mustRegistry(t *testing.T, pages []pageDef, slots []slotDef, tests []testDef) *registry {
	t.Helper()
	reg, err := newRegistry(testFiles(t), pages, slots, tests)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}
