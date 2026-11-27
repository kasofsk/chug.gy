package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func policyText(date string) string {
	return "# Terms of Service\n\nEffective " + date + "\n\nThe text of " + date + ".\n\n## A heading\n"
}

var (
	textA = policyText("September 15, 2026") // 2026-09-15
	textB = policyText("October 1, 2026")    // 2026-10-01
	textC = policyText("December 1, 2026")   // 2026-12-01
)

func threeFiles() fstest.MapFS {
	return fstest.MapFS{
		"README.md":           {Data: []byte("readme")},
		"terms/2026-09-15.md": {Data: []byte(textA)},
		"terms/2026-10-01.md": {Data: []byte(textB)},
		"terms/2026-12-01.md": {Data: []byte(textC)},
	}
}

func threeDefs() []policyDef {
	return []policyDef{
		{Slug: "terms", Title: "Terms of Service", Short: "Terms", Versions: []versionDef{
			{"2026-10-01", digestOf(textB)}, {"2026-09-15", digestOf(textA)}, {"2026-12-01", digestOf(textC)},
		}},
		{Slug: "privacy", Title: "Privacy Policy", Short: "Privacy"},
	}
}

func TestLoadPoliciesRefusals(t *testing.T) {
	with := func(f func(files fstest.MapFS, defs []policyDef)) (fs.FS, []policyDef) {
		files, defs := threeFiles(), threeDefs()
		f(files, defs)
		return files, defs
	}
	cases := map[string]struct {
		mod  func(files fstest.MapFS, defs []policyDef)
		want string
	}{
		"absent file":  {func(f fstest.MapFS, d []policyDef) { delete(f, "terms/2026-10-01.md") }, "absent"},
		"wrong digest": {func(f fstest.MapFS, d []policyDef) { d[0].Versions[0].Digest = digestOf("x") }, "hashes to"},
		"uppercase digest": {func(f fstest.MapFS, d []policyDef) {
			d[0].Versions[0].Digest = strings.ToUpper(d[0].Versions[0].Digest)
		}, "hashes to"},
		"stray file":        {func(f fstest.MapFS, d []policyDef) { f["terms/notes.md"] = &fstest.MapFile{Data: []byte("x")} }, "neither the README"},
		"stray at root":     {func(f fstest.MapFS, d []policyDef) { f["extra.txt"] = &fstest.MapFile{Data: []byte("x")} }, "neither the README"},
		"file of no policy": {func(f fstest.MapFS, d []policyDef) { f["privacy/2026-09-15.md"] = &fstest.MapFile{Data: []byte(textA)} }, "neither the README"},
		"not a date":        {func(f fstest.MapFS, d []policyDef) { d[0].Versions[0].Version = "latest" }, "a date"},
		"unpadded date":     {func(f fstest.MapFS, d []policyDef) { d[0].Versions[0].Version = "2026-9-15" }, "a date"},
		"declared twice":    {func(f fstest.MapFS, d []policyDef) { d[0].Versions = append(d[0].Versions, d[0].Versions[0]) }, "twice"},
		"slug twice":        {func(f fstest.MapFS, d []policyDef) { d[1].Slug = "terms" }, "twice"},
		"slug of a route":   {func(f fstest.MapFS, d []policyDef) { d[1].Slug = "healthz" }, "collides"},
		"slug of a prefix":  {func(f fstest.MapFS, d []policyDef) { d[1].Slug = "fonts" }, "collides"},
		"slug of a page":    {func(f fstest.MapFS, d []policyDef) { d[1].Slug = "home" }, "collides"},
		"no effective line": {func(f fstest.MapFS, d []policyDef) {
			f["terms/2026-09-15.md"] = &fstest.MapFile{Data: []byte("# T\n\nnothing\n")}
			d[0].Versions[1].Digest = digestOf("# T\n\nnothing\n")
		}, "not one"},
		"two effective lines": {func(f fstest.MapFS, d []policyDef) {
			b := textA + "\nEffective September 15, 2026\n"
			f["terms/2026-09-15.md"] = &fstest.MapFile{Data: []byte(b)}
			d[0].Versions[1].Digest = digestOf(b)
		}, "not one"},
		"draft as it stands": {func(f fstest.MapFS, d []policyDef) {
			b := policyText("[date to be set]")
			f["terms/2026-09-15.md"] = &fstest.MapFile{Data: []byte(b)}
			d[0].Versions[1].Digest = digestOf(b)
		}, "says"},
		"filed under another date": {func(f fstest.MapFS, d []policyDef) {
			d[0].Versions[1].Version = "2026-09-16"
			f["terms/2026-09-16.md"] = f["terms/2026-09-15.md"]
			delete(f, "terms/2026-09-15.md")
		}, "says"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			files, defs := with(c.mod)
			_, err := loadPolicies(files, defs, append(baseRoutes(), "/home"), "k")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error containing %q", err, c.want)
			}
		})
	}
	if _, err := loadPolicies(threeFiles(), threeDefs(), baseRoutes(), "k"); err != nil {
		t.Fatalf("the valid table was refused: %v", err)
	}
}

func policyServer(t *testing.T, files fs.FS, defs []policyDef) http.Handler {
	t.Helper()
	keepTmpdir(t)
	s, err := newServerWithPolicies(config{host: "chug.gy"}, testFiles(t), pageDefs, slotDefs, testDefs, files, defs)
	if err != nil {
		t.Fatal(err)
	}
	return s.handler()
}

func TestBadPolicyTableStopsStart(t *testing.T) {
	files, defs := threeFiles(), threeDefs()
	defs[0].Versions[0].Digest = digestOf("other")
	keepTmpdir(t)
	if _, err := newServerWithPolicies(config{host: "chug.gy"}, testFiles(t), pageDefs, slotDefs, testDefs, files, defs); err == nil {
		t.Fatal("the server started with a bad digest")
	}
	defs = threeDefs()
	defs[1].Slug = "home"
	if _, err := newServerWithPolicies(config{host: "chug.gy"}, testFiles(t), pageDefs, slotDefs, testDefs, files, defs); err == nil {
		t.Fatal("the server started with a slug a page has")
	}
}

func TestPolicyRoutes(t *testing.T) {
	h := policyServer(t, threeFiles(), threeDefs())
	for _, route := range []string{"/terms", "/terms/2026-09-15", "/terms/2026-10-01", "/terms/2026-12-01"} {
		w := get(h, "chug.gy", route)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "public, max-age=300" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s: %d %v", route, w.Code, w.Header())
		}
		faults, _ := policyFaults(w.Body.String())
		if len(faults) > 0 {
			t.Errorf("%s: %v", route, faults)
		}
		if strings.Contains(w.Body.String(), "analytics.js") {
			t.Errorf("%s loads analytics.js", route)
		}
	}
	for i, v := range []string{"2026-09-15", "2026-10-01", "2026-12-01"} {
		w := get(h, "chug.gy", "/terms/"+v+".md")
		want := []string{textA, textB, textC}[i]
		if w.Code != 200 || w.Body.String() != want || w.Header().Get("Content-Type") != "text/markdown; charset=utf-8" || w.Header().Get("Cache-Control") != "public, max-age=300" {
			t.Fatalf("%s.md: %d %v", v, w.Code, w.Header())
		}
		if digestOf(w.Body.String()) != threeDefs()[0].Versions[[]int{1, 0, 2}[i]].Digest {
			t.Errorf("%s.md does not hash to its digest", v)
		}
	}
	if get(h, "chug.gy", "/terms").Body.String() != get(h, "chug.gy", "/terms/2026-12-01").Body.String() {
		t.Error("/terms is not the newest version")
	}
	cur := get(h, "chug.gy", "/terms/2026-12-01").Body.String()
	for _, want := range []string{"<title>Terms of Service · chuggy</title>", `<a href="/terms/2026-12-01.md">This version as text</a>`,
		`<h1 id="terms-of-service">Terms of Service</h1>`, `<li><a href="/terms/2026-10-01">2026-10-01</a></li><li><a href="/terms/2026-09-15">2026-09-15</a></li>`} {
		if !strings.Contains(cur, want) {
			t.Errorf("the current page lacks %s", want)
		}
	}
	if strings.Contains(cur, "legal-earlier") || strings.Contains(cur, "2026-12-01</a></li>") {
		t.Error("the current page carries a notice or lists itself")
	}
	old := get(h, "chug.gy", "/terms/2026-09-15").Body.String()
	for _, want := range []string{`<p class="legal-earlier">This is an earlier version. <a href="/terms">Version 2026-12-01</a> replaced it.</p>`,
		`<li><a href="/terms/2026-12-01">2026-12-01</a></li><li><a href="/terms/2026-10-01">2026-10-01</a></li>`} {
		if !strings.Contains(old, want) {
			t.Errorf("the earlier page lacks %s", want)
		}
	}
	for _, route := range []string{"/terms/2026-01-01", "/terms/", "/terms/2026-12-01/", "/terms/2026-12-01.txt", "/terms/2026-01-01.md",
		"/privacy", "/privacy/", "/privacy/2026-12-01", "/privacy/2026-12-01.md"} {
		if w := get(h, "chug.gy", route); w.Code != 404 {
			t.Errorf("%s answered %d", route, w.Code)
		}
	}
}

func TestPolicyFooters(t *testing.T) {
	privacy := policyText("November 2, 2026")
	files := threeFiles()
	files["privacy/2026-11-02.md"] = &fstest.MapFile{Data: []byte(privacy)}
	both := threeDefs()
	both[1].Versions = []versionDef{{"2026-11-02", digestOf(privacy)}}
	onlyPrivacy := threeDefs()
	onlyPrivacy[0].Versions = nil
	onlyPrivacy[1].Versions = both[1].Versions
	none := threeDefs()
	none[0].Versions = nil
	marked := func(slug, short string) string {
		return `<a data-track="foot-` + slug + `" href="/` + slug + `">` + short + `</a>`
	}
	unmarked := func(slug, short string) string { return `<a href="/` + slug + `">` + short + `</a>` }
	for name, c := range map[string]struct {
		defs         []policyDef
		terms, privs bool
	}{"both": {both, true, true}, "privacy only": {onlyPrivacy, false, true}, "none": {none, false, false}} {
		fs := fstest.MapFS{}
		for k, v := range files {
			if c.terms || !strings.HasPrefix(k, "terms/") {
				fs[k] = v
			}
		}
		if name == "none" {
			fs = fstest.MapFS{"README.md": files["README.md"]}
			c.defs[1].Versions = nil
		}
		h := policyServer(t, fs, c.defs)
		check := func(route string, mk func(string, string) string, pages ...string) {
			body := get(h, "chug.gy", route).Body.String()
			if get(h, "chug.gy", route).Code != 200 {
				t.Fatalf("%s/%s: %d", name, route, get(h, "chug.gy", route).Code)
			}
			links := regexp.MustCompile(`<a [^>]*>(Terms|Privacy)</a>`).FindAllString(body, -1)
			var want []string
			if c.terms {
				want = append(want, mk("terms", "Terms"))
			}
			if c.privs {
				want = append(want, mk("privacy", "Privacy"))
			}
			if strings.Join(links, "|") != strings.Join(want, "|") {
				t.Errorf("%s %s: footer links %v, want %v", name, route, links, want)
			}
			if len(want) > 0 {
				i := strings.LastIndex(body, `llms.txt</a>`)
				j := strings.LastIndex(body, `Sign in</a>`)
				k := strings.Index(body, want[0])
				if !(i < k && k < j) {
					t.Errorf("%s %s: the links are not between llms.txt and Sign in", name, route)
				}
			}
		}
		check("/orchestration", marked)
		check("/home", marked)
		if strings.Contains(get(h, "chug.gy", "/engineering").Body.String(), `href="/terms"`) {
			t.Errorf("%s: engineering links a policy", name)
		}
		if c.terms {
			check("/terms", unmarked)
		}
		if c.privs {
			check("/privacy", unmarked)
		}
	}
}

func TestPolicySlotsRefused(t *testing.T) {
	for _, route := range []string{"/terms", "/privacy", "/terms/x"} {
		if !collides(route, fixedRoutes()) {
			t.Errorf("%s is not held by the policies", route)
		}
	}
	for _, d := range []pageDef{{Name: "home", Path: "/terms"}} {
		if _, err := newRegistry(testFiles(t), []pageDef{d}, nil, nil); err == nil {
			t.Error("a page at /terms was accepted")
		}
	}
	if _, err := newRegistry(testFiles(t), twoPages, []slotDef{{Route: "/privacy/x", Page: "home"}}, nil); err == nil {
		t.Error("a slot under /privacy/ was accepted")
	}
}

// The binary's own table and files: it starts, publishes nothing yet, and
// whatever it serves keeps to the policy.
func TestBinaryPolicies(t *testing.T) {
	s, h, _ := testServer(t, false)
	for _, d := range policyDefs {
		for _, route := range []string{"/" + d.Slug, "/" + d.Slug + "/"} {
			if len(d.Versions) == 0 && get(h, "chug.gy", route).Code != 404 {
				t.Errorf("%s is served with no version", route)
			}
		}
		for _, v := range d.Versions {
			for _, route := range []string{"/" + d.Slug, "/" + d.Slug + "/" + v.Version} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", route, nil))
				checkPolicyAndLoads(t, h, route, w.Body.String())
			}
		}
	}
	if len(s.policies.links) != 0 && len(policyDefs[0].Versions)+len(policyDefs[1].Versions) == 0 {
		t.Error("a footer link with no version")
	}
}
