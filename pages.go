package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// A page is one html/template under site/pages/ that owns its whole document,
// registered here by a short stable name. Path is its own URL, required and
// never "/": it is always served there outside any test, even when it is also
// an arm. Stylesheet, if set, names a file under site/ that only this page loads.
type pageDef struct {
	Name       string
	Path       string
	Stylesheet string
}

// A test splits the visitors of a route between pages. Its route is that of
// the one slot that serves it. The first arm is the baseline. To change a
// test's arms, give it a new ID.
type testDef struct {
	ID   string
	Arms []arm
}

// A slot says what a shared route, such as "/", serves: exactly one of a page
// (by name), shown there outside any test, or a test (by ID).
type slotDef struct {
	Route string
	Page  string
	Test  string
}

type arm struct {
	Page   string
	Weight int
}

var pageDefs = []pageDef{
	{Name: "home", Path: "/home"},
	{Name: "orchestration", Path: "/orchestration", Stylesheet: "orchestration.css"},
}

var slotDefs = []slotDef{
	{Route: "/", Test: "home-redesign"},
}

var testDefs = []testDef{
	{ID: "home-redesign", Arms: []arm{{"home", 1}, {"orchestration", 1}}},
}

// What every page is rendered from.
type pageData struct {
	Name           string
	Canonical      string
	Preview        bool   // an admin's preview: the page's script sends nothing
	Test           string // the test being shown, empty outside one
	Arm            string // the arm being shown, empty outside a test
	StylesKey      string // cache key of /site.css
	PageStylesheet string // the page's own stylesheet's path, or empty
	PageStylesKey  string // that stylesheet's cache key
}

type page struct {
	def  pageDef
	tmpl *template.Template
	css  []byte
	key  string
}

type registry struct {
	pages map[string]*page
	tests map[string]testDef // by ID
	slots []slotDef          // in the order declared
	route map[string]string  // a declared test's ID -> the route of its slot
	order []testDef          // in the order declared
}

var (
	idPattern   = regexp.MustCompile(`^[a-z0-9-]+$`)
	pathPattern = regexp.MustCompile(`^/[A-Za-z0-9._~/-]*$`)
)

// The routes the binary serves whatever the pages are, with those ending in
// "/" standing for everything below them.
func fixedRoutes() []string {
	fixed := []string{"/requested", "/request-access", "/admin", "/admin/", "/healthz",
		"/e", "/site.css", "/site.js", "/analytics.js", "/robots.txt", "/favicon.svg", "/fonts/", "/img/", "/docs", "/docs/", "/llms.txt"}
	for from := range redirects {
		fixed = append(fixed, from)
	}
	for from := range docsRedirects {
		fixed = append(fixed, from)
	}
	return fixed
}

func collides(route string, fixed []string) bool {
	for _, f := range fixed {
		if route == f || (strings.HasSuffix(f, "/") && strings.HasPrefix(route, f)) {
			return true
		}
	}
	return false
}

func validPath(p string) bool {
	return pathPattern.MatchString(p) && (p == "/" || !strings.HasSuffix(p, "/")) && !strings.Contains(p, "//")
}

// Checks the declarations and loads the pages, or says what is wrong.
func newRegistry(files fs.FS, pdefs []pageDef, sdefs []slotDef, tdefs []testDef) (*registry, error) {
	reg := &registry{pages: map[string]*page{}, tests: map[string]testDef{}, route: map[string]string{}}
	fixed := fixedRoutes()
	taken := map[string]string{} // route -> what holds it
	claim := func(route, who string) error {
		if !validPath(route) {
			return fmt.Errorf("%s: %q is not a path", who, route)
		}
		if collides(route, fixed) {
			return fmt.Errorf("%s: %q collides with a fixed route", who, route)
		}
		if other, ok := taken[route]; ok {
			return fmt.Errorf("%s: %q collides with %s", who, route, other)
		}
		taken[route] = who
		return nil
	}
	for _, d := range pdefs {
		if !idPattern.MatchString(d.Name) {
			return nil, fmt.Errorf("page %q: a name is lowercase letters, digits and hyphens", d.Name)
		}
		if _, ok := reg.pages[d.Name]; ok {
			return nil, fmt.Errorf("page %q is registered twice", d.Name)
		}
		if d.Path == "" {
			return nil, fmt.Errorf("page %q has no path of its own", d.Name)
		}
		if d.Path == "/" {
			return nil, fmt.Errorf("page %q: its own path cannot be \"/\"; a slot says what \"/\" serves", d.Name)
		}
		pg := &page{def: d}
		base, err := template.ParseFS(files, "partials/*.html")
		if err != nil {
			return nil, err
		}
		if pg.tmpl, err = base.ParseFS(files, "pages/"+d.Name+".html"); err != nil {
			return nil, fmt.Errorf("page %q: %w", d.Name, err)
		}
		if d.Stylesheet != "" {
			if pg.css, err = fs.ReadFile(files, d.Stylesheet); err != nil {
				return nil, fmt.Errorf("page %q: %w", d.Name, err)
			}
			sum := sha256.Sum256(pg.css)
			pg.key = hex.EncodeToString(sum[:8])
			if err := claim("/"+d.Stylesheet, "the stylesheet of page "+d.Name); err != nil {
				return nil, err
			}
		}
		if err := claim(d.Path, "page "+d.Name); err != nil {
			return nil, err
		}
		reg.pages[d.Name] = pg
	}
	for _, t := range tdefs {
		if !idPattern.MatchString(t.ID) {
			return nil, fmt.Errorf("test %q: an ID is lowercase letters, digits and hyphens", t.ID)
		}
		if _, ok := reg.tests[t.ID]; ok {
			return nil, fmt.Errorf("test %q is declared twice", t.ID)
		}
		if len(t.Arms) < 2 {
			return nil, fmt.Errorf("test %q needs at least two arms", t.ID)
		}
		for _, a := range t.Arms {
			if reg.pages[a.Page] == nil {
				return nil, fmt.Errorf("test %q: arm %q is not a registered page", t.ID, a.Page)
			}
			if a.Weight <= 0 {
				return nil, fmt.Errorf("test %q: arm %q has weight %d", t.ID, a.Page, a.Weight)
			}
		}
		reg.tests[t.ID] = t
		reg.order = append(reg.order, t)
	}
	slotAt := map[string]bool{}
	for _, sl := range sdefs {
		who := fmt.Sprintf("slot %q", sl.Route)
		if !validPath(sl.Route) {
			return nil, fmt.Errorf("%s: the route is not a path", who)
		}
		switch {
		case sl.Page == "" && sl.Test == "":
			return nil, fmt.Errorf("%s names neither a page nor a test", who)
		case sl.Page != "" && sl.Test != "":
			return nil, fmt.Errorf("%s names both a page and a test", who)
		case sl.Page != "" && reg.pages[sl.Page] == nil:
			return nil, fmt.Errorf("%s: page %q is not a registered page", who, sl.Page)
		case sl.Test != "":
			if _, ok := reg.tests[sl.Test]; !ok {
				return nil, fmt.Errorf("%s: test %q is not a declared test", who, sl.Test)
			}
			if other, ok := reg.route[sl.Test]; ok {
				return nil, fmt.Errorf("%s: test %q is already served by slot %q", who, sl.Test, other)
			}
		}
		if slotAt[sl.Route] {
			return nil, fmt.Errorf("%s is declared twice", who)
		}
		if err := claim(sl.Route, who); err != nil {
			return nil, err
		}
		slotAt[sl.Route] = true
		if sl.Test != "" {
			reg.route[sl.Test] = sl.Route
		}
		reg.slots = append(reg.slots, sl)
	}
	for _, t := range tdefs {
		if _, ok := reg.route[t.ID]; !ok {
			return nil, fmt.Errorf("test %q is served by no slot", t.ID)
		}
	}
	return reg, nil
}

// Draws an arm's page name by weight.
func (t testDef) draw() string {
	total := 0
	for _, a := range t.Arms {
		total += a.Weight
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(total)))
	if err != nil {
		panic(err)
	}
	pick := int(n.Int64())
	for _, a := range t.Arms {
		if pick < a.Weight {
			return a.Page
		}
		pick -= a.Weight
	}
	return t.Arms[0].Page
}

func (t testDef) hasArm(name string) bool {
	for _, a := range t.Arms {
		if a.Page == name {
			return true
		}
	}
	return false
}

func cookieName(testID string) string { return "chug_test_" + testID }

func (s *server) canonicalURL(route string) string {
	host := s.cfg.host
	if host == "" {
		host = "chug.gy"
	}
	return "https://" + host + route
}

// Serves a page outside any test, at its own path or at a slot that names it:
// no test cookie is read or set, even when the page is an arm.
func (s *server) servePage(p *page, route string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=300")
		s.render(w, p, route, "", "", false)
	}
}

// Serves a test's route: the visitor's arm, drawn once and kept in a cookie.
func (s *server) serveTest(t testDef, route string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "private, no-store")
		h.Add("Vary", "Cookie")
		name := ""
		if c, err := r.Cookie(cookieName(t.ID)); err == nil && t.hasArm(c.Value) {
			name = c.Value
		}
		if name == "" {
			name = t.draw()
			host := r.Host
			if hh, _, err := net.SplitHostPort(host); err == nil {
				host = hh
			}
			http.SetCookie(w, &http.Cookie{
				Name:     cookieName(t.ID),
				Value:    name,
				Path:     "/",
				MaxAge:   int((90 * 24 * time.Hour).Seconds()),
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				Secure:   !isLoopback(host),
			})
		}
		s.render(w, s.reg.pages[name], route, t.ID, name, false)
	}
}

func (s *server) render(w http.ResponseWriter, p *page, route, test, armName string, preview bool) {
	data := pageData{
		Name:      p.def.Name,
		Canonical: s.canonicalURL(route),
		Preview:   preview,
		Test:      test,
		Arm:       armName,
		StylesKey: stylesKey,
	}
	if p.def.Stylesheet != "" {
		data.PageStylesheet = "/" + p.def.Stylesheet
		data.PageStylesKey = p.key
	}
	var buf bytes.Buffer
	if err := p.tmpl.ExecuteTemplate(&buf, p.def.Name+".html", data); err != nil {
		http.Error(w, "the page could not be drawn", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}

func (s *server) pageStylesheet(p *page) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		if r.URL.Query().Get("v") == p.key {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		w.Write(p.css)
	}
}

// Mounts every page at its own path, outside any test, and each slot's route
// with what it names: a page outside any test, or a test's drawn arm.
func (s *server) mountPages(mux *http.ServeMux) {
	pattern := func(route string) string {
		if route == "/" {
			return "GET /{$}"
		}
		return "GET " + route
	}
	for _, sl := range s.reg.slots {
		if sl.Test != "" {
			mux.HandleFunc(pattern(sl.Route), s.serveTest(s.reg.tests[sl.Test], sl.Route))
		} else {
			mux.HandleFunc(pattern(sl.Route), s.servePage(s.reg.pages[sl.Page], sl.Route))
		}
	}
	for _, p := range s.reg.pages {
		mux.HandleFunc(pattern(p.def.Path), s.servePage(p, p.def.Path))
		if p.def.Stylesheet != "" {
			mux.HandleFunc("GET /"+p.def.Stylesheet, s.pageStylesheet(p))
		}
	}
}

// What the form says about where it came from, kept only where it checks out.
func (s *server) origin(r *http.Request) (pg, test, armName string) {
	posted := strings.TrimSpace(r.PostFormValue("page"))
	if s.reg.pages[posted] == nil {
		return "", "", ""
	}
	pg = posted
	id := strings.TrimSpace(r.PostFormValue("test"))
	if t, ok := s.reg.tests[id]; ok {
		if c, err := r.Cookie(cookieName(id)); err == nil && c.Value == pg && t.hasArm(pg) {
			return pg, id, c.Value
		}
	}
	return pg, "", ""
}
