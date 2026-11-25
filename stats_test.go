package main

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Wilson intervals as published: Newcombe (1998) for 81/263 and 15/148, and
// the closed forms for 0 of 20 and 10 of 10.
func TestWilsonAgainstPublishedValues(t *testing.T) {
	for _, c := range []struct {
		k, n      int
		low, high float64
	}{
		{81, 263, 0.2553, 0.3662},
		{15, 148, 0.0624, 0.1605},
		{0, 20, 0, 0.1611},
		{10, 10, 0.7225, 1},
		{5, 10, 0.2366, 0.7634},
	} {
		low, high := wilson(c.k, c.n)
		if math.Abs(low-c.low) > 0.00006 || math.Abs(high-c.high) > 0.00006 {
			t.Errorf("wilson(%d, %d) = %.4f, %.4f; published %.4f, %.4f", c.k, c.n, low, high, c.low, c.high)
		}
	}
	if low, high := wilson(0, 0); low != 0 || high != 0 {
		t.Errorf("wilson(0, 0) = %v, %v", low, high)
	}
}

func TestChanceToBeat(t *testing.T) {
	same := chanceToBeat("x", 500, 1000, 500, 1000)
	if math.Abs(same-0.5) > 0.03 {
		t.Errorf("identical arms: %.3f", same)
	}
	if better := chanceToBeat("x", 100, 1000, 200, 1000); better < 0.99 {
		t.Errorf("a clearly better arm: %.3f", better)
	}
	if worse := chanceToBeat("x", 200, 1000, 100, 1000); worse > 0.01 {
		t.Errorf("a clearly worse arm: %.3f", worse)
	}
	if chanceToBeat("x", 7, 90, 12, 95) != chanceToBeat("x", 7, 90, 12, 95) {
		t.Error("the same data gave two figures")
	}
	if c := chanceToBeat("x", 0, 0, 0, 0); math.Abs(c-0.5) > 0.03 {
		t.Errorf("no data: %.3f", c)
	}
}

// ------------------------------------------------------------------ pages --

type ev struct {
	day, kind, visitor                 string
	path, test, arm                    string
	referrer, source, medium, campaign string
	target, country, device            string
	value                              int
}

func seed(t *testing.T, s *server, evs ...ev) {
	t.Helper()
	for _, e := range evs {
		if e.path == "" && e.kind == "view" {
			e.path = "/"
		}
		_, err := s.events.db.Exec(insertEvent, e.day+"T12:00:00Z", e.day, e.kind, "home", e.path, e.test, e.arm, e.referrer,
			e.source, e.medium, e.campaign, e.target, e.value, e.visitor, e.country, e.device)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// An admin environment whose registry has two declared tests, and whose clock
// says 2026-06-15.
func statsEnv(t *testing.T) (*adminEnv, *browser) {
	t.Helper()
	e := newAdminEnv(t)
	e.s.events.close()
	keepTmpdir(t)
	tests := []testDef{
		{ID: "hero-1", Route: "/", Arms: []arm{{"home", 1}, {"alt", 1}}},
		{ID: "quiet-1", Route: "/quiet", Arms: []arm{{"alt", 1}, {"home", 1}}},
	}
	s, err := newServerWith(e.cfg, testFiles(t), twoPages, tests)
	if err != nil {
		t.Fatal(err)
	}
	s.auth.now = func() time.Time { return e.now }
	s.events.now = func() time.Time { return time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC) }
	e.s, e.h = s, s.handler()
	b := e.browser()
	b.mustSignIn(t)
	return e, b
}

func TestTrafficCounts(t *testing.T) {
	e, b := statsEnv(t)
	seed(t, e.s,
		ev{day: "2026-06-15", kind: "view", visitor: "v1", path: "/", referrer: "news.example", country: "US", device: "desktop"},
		ev{day: "2026-06-15", kind: "view", visitor: "v1", path: "/", referrer: "news.example", country: "US", device: "desktop"},
		ev{day: "2026-06-15", kind: "view", visitor: "v2", path: "/docs", source: "nl", medium: "email", campaign: "launch", country: "DE", device: "mobile"},
		ev{day: "2026-06-15", kind: "scroll", visitor: "v9", value: 50},
		ev{day: "2026-06-15", kind: "submit", visitor: "v8"},
		ev{day: "2026-06-13", kind: "view", visitor: "v1", path: "/", country: "US", device: "desktop"}, // the same ID, another day
		ev{day: "2026-06-08", kind: "view", visitor: "v3", path: "/", country: "", device: "desktop"},   // 8 days back
		ev{day: "2026-03-17", kind: "view", visitor: "v4", path: "/old", device: "desktop"},             // 90 days back is 03-18
		ev{day: "2026-03-18", kind: "view", visitor: "v5", path: "/old", device: "desktop"},
	)
	ctx := t.Context()
	to := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	days, err := e.s.events.daily(ctx, to.AddDate(0, 0, -6), to)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range days {
		got = append(got, d.Day[5:]+"="+itoa(d.Visitors)+"/"+itoa(d.Views))
	}
	want := "06-09=0/0 06-10=0/0 06-11=0/0 06-12=0/0 06-13=1/1 06-14=0/0 06-15=2/3"
	if strings.Join(got, " ") != want {
		t.Errorf("7 days:\n got %s\nwant %s", strings.Join(got, " "), want)
	}

	rows := func(dim, back int) string {
		r, err := e.s.events.breakdown(ctx, dim, to.AddDate(0, 0, -(back-1)), to, true, topRows)
		if err != nil {
			t.Fatal(err)
		}
		var parts []string
		for _, c := range r {
			parts = append(parts, c.Key+"="+itoa(c.Visitors)+"/"+itoa(c.Views))
		}
		return strings.Join(parts, "; ")
	}
	for _, c := range []struct {
		name      string
		dim, back int
		want      string
	}{
		{"pages", 0, 7, "/=2/3; /docs=1/1"},
		{"referrers", 1, 7, "=2/2; news.example=1/2"},
		{"campaigns", 2, 7, "nl / email / launch=1/1"},
		{"countries", 3, 7, "US=2/3; DE=1/1"},
		{"devices", 4, 7, "desktop=2/3; mobile=1/1"},
		{"pages 30", 0, 30, "/=3/4; /docs=1/1"},
		{"pages 90", 0, 90, "/=3/4; /docs=1/1; /old=1/1"},
	} {
		if got := rows(c.dim, c.back); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}

	page := b.do("GET", "/admin/traffic?days=7", nil)
	body := page.Body.String()
	for _, w := range []string{"<dt>Visitors</dt><dd>3</dd>", "<dt>Views</dt><dd>4</dd>", "2026-06-09 to 2026-06-15", "Direct", "nl / email / launch", "<svg"} {
		if !strings.Contains(body, w) {
			t.Errorf("the page lacks %q", w)
		}
	}
	if strings.Contains(body, "Show all") {
		t.Error("Show all with fewer than ten rows")
	}
	// 30 days takes in 06-08: the day before the 30 is 05-17.
	if body := b.do("GET", "/admin/traffic", nil).Body.String(); !strings.Contains(body, "<dt>Visitors</dt><dd>4</dd>") || !strings.Contains(body, "2026-05-17 to 2026-06-15") {
		t.Errorf("the default range is not 30 days ending today: %.600s", body)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestTrafficRangeControl(t *testing.T) {
	e, b := statsEnv(t)
	seed(t, e.s, ev{day: "2026-06-15", kind: "view", visitor: "v1"})
	for query, days := range map[string]int{"": 30, "?days=7": 7, "?days=30": 30, "?days=90": 90, "?days=14": 30, "?days=abc": 30, "?days=-7": 30} {
		body := b.do("GET", "/admin/traffic"+query, nil).Body.String()
		if n := strings.Count(body, "<g><title>"); n != days {
			t.Errorf("%q drew %d days, wanted %d", query, n, days)
		}
		if n := strings.Count(body, `aria-current="page"`); n != 2 { // the nav and the range
			t.Errorf("%q marks %d current links", query, n)
		}
		if !strings.Contains(body, `href="/admin/traffic?days=`+itoa(days)+`" aria-current="page"`) {
			t.Errorf("%q does not mark %d days", query, days)
		}
	}
}

func TestTrafficShowAll(t *testing.T) {
	e, b := statsEnv(t)
	for i := 1; i <= 12; i++ {
		v := "v" + itoa(i)
		seed(t, e.s, ev{day: "2026-06-15", kind: "view", visitor: v, path: "/p" + itoa(i), country: "US"})
		if i <= 11 { // /p12 is last on every tie-break
			seed(t, e.s, ev{day: "2026-06-14", kind: "view", visitor: v, path: "/p" + itoa(i), country: "US"})
		}
	}
	top := b.do("GET", "/admin/traffic?days=7", nil).Body.String()
	if n := strings.Count(top, `<td class="wrap">/p`); n != 10 {
		t.Errorf("the top shows %d pages", n)
	}
	if n := strings.Count(top, "Show all"); n != 1 || !strings.Contains(top, `href="/admin/traffic?days=7&amp;all=pages">Show all`) {
		t.Errorf("Show all appears %d times: %s", n, top)
	}
	all := b.do("GET", "/admin/traffic?days=7&all=pages", nil).Body.String()
	if n := strings.Count(all, `<td class="wrap">/p`); n != 12 {
		t.Errorf("all shows %d pages", n)
	}
	if strings.Contains(all, "Show all") || !strings.Contains(all, "Show top 10") {
		t.Error("the expanded table offers Show all")
	}
	// Only the named table expands; an unknown name expands nothing.
	if n := strings.Count(b.do("GET", "/admin/traffic?all=nope", nil).Body.String(), `<td class="wrap">/p`); n != 10 {
		t.Errorf("an unknown table name showed %d", n)
	}
}

func TestChartIsPlainSVG(t *testing.T) {
	e, b := statsEnv(t)
	seed(t, e.s, ev{day: "2026-06-15", kind: "view", visitor: "v1"})
	w := b.do("GET", "/admin/traffic", nil)
	body := w.Body.String()
	for _, bad := range []string{"<script", " style=", "<style", "fill=\"#", "stroke=\"#"} {
		if strings.Contains(body, bad) {
			t.Errorf("the page contains %q", bad)
		}
	}
	if !strings.Contains(body, `class="visitors"`) || !strings.Contains(body, `class="views"`) {
		t.Error("the chart lacks its bars")
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Errorf("the policy changed: %s", w.Header().Get("Content-Security-Policy"))
	}
}

// ------------------------------------------------------------------ tests --

func TestTestsCounts(t *testing.T) {
	e, _ := statsEnv(t)
	h := func(d, kind, v, arm string, value int) ev {
		return ev{day: d, kind: kind, visitor: v, test: "hero-1", arm: arm, value: value}
	}
	seed(t, e.s,
		// home, the baseline
		h("2026-06-01", "view", "a1", "home", 0), h("2026-06-01", "engaged", "a1", "home", 4), h("2026-06-01", "engaged", "a1", "home", 7),
		h("2026-06-02", "view", "a1", "home", 0), // the same ID on another day
		h("2026-06-02", "view", "a2", "home", 0), h("2026-06-02", "scroll", "a2", "home", 50),
		h("2026-06-02", "view", "a3", "home", 0), h("2026-06-02", "scroll", "a3", "home", 75), h("2026-06-02", "form_start", "a3", "home", 0), h("2026-06-02", "submit", "a3", "home", 0),
		h("2026-06-02", "view", "a4", "home", 0), h("2026-06-02", "engaged", "a4", "home", 9),
		h("2026-06-09", "form_start", "a5", "home", 0), h("2026-06-09", "submit", "a5", "home", 0), // no view that day
		h("2026-06-09", "engaged", "a5", "home", 30), h("2026-06-09", "scroll", "a5", "home", 100),
		h("2026-06-03", "view", "a6", "home", 0),
		ev{day: "2026-06-03", kind: "click", visitor: "a6", test: "hero-1", arm: "home", target: "cta"},
		ev{day: "2026-06-03", kind: "click", visitor: "a6", test: "hero-1", arm: "home", target: "cta"},
		ev{day: "2026-06-03", kind: "click", visitor: "a6", test: "hero-1", arm: "home", target: "nav"},
		ev{day: "2026-06-03", kind: "click", visitor: "a5", test: "hero-1", arm: "home", target: "nav"}, // no view
		// a page visit that is not a test visit
		ev{day: "2026-06-04", kind: "view", visitor: "a7"}, ev{day: "2026-06-04", kind: "submit", visitor: "a7"},
		// alt
		h("2026-06-04", "view", "b1", "alt", 0), ev{day: "2026-06-04", kind: "click", visitor: "b1", test: "hero-1", arm: "alt", target: "cta"},
		h("2026-06-04", "view", "b2", "alt", 0), h("2026-06-04", "scroll", "b2", "alt", 75),
		// an ended test, its arms out of order
		ev{day: "2026-05-01", kind: "view", visitor: "z1", test: "old-1", arm: "zeta", path: "/old"},
		ev{day: "2026-05-02", kind: "view", visitor: "z2", test: "old-1", arm: "alpha", path: "/old"},
		ev{day: "2026-05-02", kind: "submit", visitor: "z2", test: "old-1", arm: "alpha"},
		ev{day: "2026-05-02", kind: "click", visitor: "z2", test: "old-1", arm: "alpha", target: "x"},
	)
	secs, err := e.s.testSections(httpReq(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != 3 || secs[0].ID != "hero-1" || secs[1].ID != "quiet-1" || secs[2].ID != "old-1" {
		t.Fatalf("sections: %+v", secs)
	}
	hero := secs[0]
	if hero.Route != "/" || hero.Ended || hero.First != "2026-06-01" || hero.Last != "2026-06-09" {
		t.Errorf("hero: %+v", hero)
	}
	if len(hero.Arms) != 2 || hero.Arms[0].Name != "home" || !hero.Arms[0].Baseline || hero.Arms[1].Name != "alt" || hero.Arms[1].Baseline {
		t.Fatalf("arms: %+v", hero.Arms)
	}
	counts := func(a armRow) string {
		out := []string{itoa(a.Visitors)}
		for _, r := range a.Rates {
			out = append(out, itoa(r.Count))
		}
		for _, r := range a.Clicks {
			out = append(out, itoa(r.Count))
		}
		return strings.Join(out, " ")
	}
	// visitors, engaged, reached 75, form starts, submissions, then clicks (cta, nav)
	if got := counts(hero.Arms[0]); got != "6 3 1 1 1 1 1" {
		t.Errorf("home: %s", got)
	}
	if got := counts(hero.Arms[1]); got != "2 1 1 0 0 1 0" {
		t.Errorf("alt: %s", got)
	}
	if hero.Arms[0].Rates[0].Chance != "" || hero.Arms[1].Rates[0].Chance == "" || hero.Arms[1].Clicks[0].Chance != "" {
		t.Error("chance belongs to the rates of arms that are not the baseline")
	}
	if hero.Arms[0].Rates[0].Rate != "50.0%" || hero.Arms[0].Rates[0].Interval != "18.8–81.2%" {
		t.Errorf("rate 3/6: %+v", hero.Arms[0].Rates[0])
	}
	quiet := secs[1]
	if quiet.First != "" || quiet.Arms[0].Name != "alt" || quiet.Arms[1].Name != "home" || quiet.Arms[0].Visitors != 0 || !quiet.Arms[0].Rates[0].NoVisitor {
		t.Errorf("quiet: %+v", quiet)
	}
	old := secs[2]
	if !old.Ended || old.Route != "/old" || old.First != "2026-05-01" || old.Last != "2026-05-02" ||
		old.Arms[0].Name != "alpha" || old.Arms[1].Name != "zeta" || old.Arms[0].Baseline {
		t.Errorf("old: %+v", old)
	}
	for _, a := range old.Arms {
		for _, r := range a.Rates {
			if r.Chance != "" {
				t.Error("an ended test shows a chance")
			}
		}
	}
	if got := counts(old.Arms[0]); got != "1 0 0 0 1 1" {
		t.Errorf("alpha: %s", got)
	}
}

func TestTestsPageOrderAndLabels(t *testing.T) {
	e, b := statsEnv(t)
	seed(t, e.s,
		ev{day: "2026-05-01", kind: "view", visitor: "z1", test: "old-1", arm: "zeta", path: "/old"},
		ev{day: "2026-05-02", kind: "view", visitor: "q1", test: "hero-1", arm: "home"},
	)
	w := b.do("GET", "/admin/tests", nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	body := w.Body.String()
	i, j, k := strings.Index(body, "hero-1"), strings.Index(body, "quiet-1"), strings.Index(body, "old-1")
	if !(0 < i && i < j && j < k) {
		t.Errorf("the order is %d %d %d", i, j, k)
	}
	if n := strings.Count(body, "Ended"); n != 1 {
		t.Errorf("Ended appears %d times", n)
	}
	if !strings.Contains(body, "<code>/old</code>") || !strings.Contains(body, "2026-05-01 to 2026-05-01") {
		t.Error("the ended test lacks its route and dates")
	}
	if !strings.Contains(body, "Too few visitors") || strings.Contains(body, "winner") {
		t.Error("labels")
	}
	if again := b.do("GET", "/admin/tests", nil).Body.String(); again != body {
		t.Error("a reload changed the page")
	}
}

func TestFewVisitorsAreGreyed(t *testing.T) {
	counts := &testCounts{First: "a", Last: "b", Arms: map[string]*armCounts{
		"base": {Visitors: 200, Engaged: 100},
		"few":  {Visitors: 99, Engaged: 90},
		"ok":   {Visitors: 100, Engaged: 90},
	}}
	sec := buildSection("t", "/", false, []string{"base", "few", "ok"}, counts)
	for _, c := range []struct {
		arm   int
		few   bool
		greys bool
	}{{0, false, false}, {1, true, true}, {2, false, false}} {
		a := sec.Arms[c.arm]
		if a.Few != c.few || a.Rates[0].Greyed != c.greys {
			t.Errorf("%s: few %v greyed %v", a.Name, a.Few, a.Rates[0].Greyed)
		}
	}
	if sec.Arms[2].Rates[0].Rate != "90.0%" {
		t.Errorf("rate %s", sec.Arms[2].Rates[0].Rate)
	}
}

func TestStatsPagesNeedSignIn(t *testing.T) {
	e, _ := statsEnv(t)
	for _, p := range []string{"/admin/traffic", "/admin/tests"} {
		if w := e.browser().do("GET", p, nil); w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), testIssuer+"/oauth2/auth") {
			t.Errorf("%s without a session: %d %s", p, w.Code, w.Header().Get("Location"))
		}
	}
}

func TestStatsPagesRefusedByKeto(t *testing.T) {
	e, b := statsEnv(t)
	e.keto.set(false)
	e.now = e.now.Add(2 * ketoTTL)
	for _, p := range []string{"/admin/traffic", "/admin/tests"} {
		if w := b.do("GET", p, nil); w.Code != 403 {
			t.Errorf("%s after the permit went: %d", p, w.Code)
		}
	}
}

func httpReq(t *testing.T) *http.Request {
	return httptest.NewRequestWithContext(t.Context(), "GET", "/admin/tests", nil)
}

// A database made by schema version 1 gains the Tests page's index and keeps
// its events.
func TestMigrationFromVersionOne(t *testing.T) {
	dir := t.TempDir()
	keepTmpdir(t)
	st, err := openEventStore(dir, "chug.gy")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP INDEX events_tests`, `CREATE INDEX events_test_arm ON events (test, arm, kind, day)`, `PRAGMA user_version = 1`} {
		if _, err := st.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.db.Exec(insertEvent, "2026-06-01T00:00:00Z", "2026-06-01", "view", "home", "/", "", "", "", "", "", "", "", 0, "v", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := migrateEvents(st.db); err != nil {
		t.Fatal(err)
	}
	var version, n, indexes int
	st.db.QueryRow("PRAGMA user_version").Scan(&version)
	st.db.QueryRow("SELECT COUNT(*) FROM events").Scan(&n)
	st.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name IN ('events_tests', 'events_test_arm')").Scan(&indexes)
	if version != 2 || n != 1 || indexes != 1 {
		t.Errorf("version %d, %d events, %d of the test indexes", version, n, indexes)
	}
	st.close()
}
