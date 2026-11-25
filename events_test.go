package main

import (
	"database/sql"
	"fmt"
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

const desktopUA = "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0"

// A server with one test over two pages, its data in a temp directory.
func eventServer(t *testing.T) (*server, http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	files := fstest.MapFS{
		"pages/home.html":    {Data: []byte(`<html data-page="{{.Name}}">{{template "form" .}}</html>`)},
		"pages/home-b.html":  {Data: []byte(`<html data-page="{{.Name}}">{{template "form" .}}</html>`)},
		"partials/form.html": {Data: []byte(`{{define "form"}}<form></form>{{end}}`)},
	}
	pages := []pageDef{{Name: "home", Path: "/"}, {Name: "home-b"}}
	tests := []testDef{{ID: "hero-1", Route: "/", Arms: []arm{{"home", 1}, {"home-b", 1}}}}
	keepTmpdir(t)
	s, err := newServerWith(config{dataDir: dir, host: "chug.gy"}, files, pages, tests)
	if err != nil {
		t.Fatal(err)
	}
	return s, s.handler(), dir
}

func postEvent(h http.Handler, body, ua, contentType string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/e", strings.NewReader(body))
	r.Host = "chug.gy"
	r.RemoteAddr = "203.0.113.7:5555"
	r.Header.Set("User-Agent", ua)
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

type row struct {
	At, Day, Kind, Page, Path, Test, Arm, Referrer, Source, Medium, Campaign, Target string
	Value                                                                            int
	Visitor, Country, Device                                                         string
}

// Flushes the queue and reads every stored row.
func stored(t *testing.T, s *server, dir string) []row {
	t.Helper()
	if err := s.events.close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT at, day, kind, page, path, test, arm, referrer, utm_source, utm_medium, utm_campaign, target, value, visitor, country, device FROM events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.At, &r.Day, &r.Kind, &r.Page, &r.Path, &r.Test, &r.Arm, &r.Referrer, &r.Source, &r.Medium, &r.Campaign, &r.Target, &r.Value, &r.Visitor, &r.Country, &r.Device); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestEveryKindEndToEnd(t *testing.T) {
	s, h, dir := eventServer(t)
	events := []string{
		`{"kind":"view","page":"home","path":"/","referrer":"https://news.example.org/a/b?x=1","utm_source":"hn","utm_medium":"post","utm_campaign":"launch","test":"hero-1","arm":"home"}`,
		`{"kind":"scroll","page":"home","path":"/","value":50}`,
		`{"kind":"engaged","page":"home","path":"/","value":12}`,
		`{"kind":"click","page":"home","path":"/","target":"cta-hero-request"}`,
		`{"kind":"form_start","page":"home-b","path":"/","test":"hero-1","arm":"home-b"}`,
	}
	for _, e := range events {
		// A beacon sends text/plain; so does the fetch fallback.
		if w := postEvent(h, e, desktopUA, "text/plain;charset=UTF-8", map[string]string{"CF-IPCountry": "DE"}); w.Code != 204 {
			t.Fatalf("%s answered %d: %s", e, w.Code, w.Body)
		}
	}
	rows := stored(t, s, dir)
	if len(rows) != 5 {
		t.Fatalf("stored %d rows: %+v", len(rows), rows)
	}
	v := rows[0]
	if v.Kind != "view" || v.Referrer != "news.example.org" || v.Source != "hn" || v.Medium != "post" || v.Campaign != "launch" ||
		v.Test != "hero-1" || v.Arm != "home" || v.Country != "DE" || v.Device != "desktop" || v.Day != time.Now().UTC().Format("2006-01-02") {
		t.Errorf("view stored as %+v", v)
	}
	if rows[1].Value != 50 || rows[2].Value != 12 || rows[3].Target != "cta-hero-request" || rows[3].Test != "" || rows[4].Page != "home-b" {
		t.Errorf("rows %+v", rows[1:])
	}
	if len(v.Visitor) != 32 {
		t.Errorf("visitor %q is not 16 bytes of hex", v.Visitor)
	}
}

func TestEventRefusals(t *testing.T) {
	s, h, dir := eventServer(t)
	big := `{"kind":"view","page":"home","path":"/` + strings.Repeat("a", 5000) + `"}`
	for name, body := range map[string]string{
		"not json":         `view`,
		"empty":            ``,
		"unknown kind":     `{"kind":"hover","page":"home"}`,
		"submit from page": `{"kind":"submit","page":"home"}`,
		"unknown page":     `{"kind":"view","page":"nope"}`,
		"unknown field":    `{"kind":"view","page":"home","email":"a@b.co"}`,
		"test without arm": `{"kind":"view","page":"home","test":"hero-1"}`,
		"arm without test": `{"kind":"view","page":"home","arm":"home"}`,
		"unknown test":     `{"kind":"view","page":"home","test":"zz","arm":"home"}`,
		"unknown arm":      `{"kind":"view","page":"home","test":"hero-1","arm":"home-c"}`,
		"long string":      `{"kind":"view","page":"home","utm_source":"` + strings.Repeat("a", 201) + `"}`,
		"long path":        `{"kind":"view","page":"home","path":"/` + strings.Repeat("a", 501) + `"}`,
		"bad scroll":       `{"kind":"scroll","page":"home","value":30}`,
		"zero engaged":     `{"kind":"engaged","page":"home","value":0}`,
		"click no target":  `{"kind":"click","page":"home"}`,
		"two events":       `{"kind":"view","page":"home"}{"kind":"view","page":"home"}`,
		"over 4 KiB":       big,
	} {
		if w := postEvent(h, body, desktopUA, "text/plain", nil); w.Code != 400 {
			t.Errorf("%s: answered %d", name, w.Code)
		}
	}
	if rows := stored(t, s, dir); len(rows) != 0 {
		t.Errorf("refused events were kept: %+v", rows)
	}
}

func TestReferrerOfThisSiteIsDropped(t *testing.T) {
	s, h, dir := eventServer(t)
	for _, ref := range []string{"https://chug.gy/docs", "https://www.chug.gy/", "javascript:alert(1)", ""} {
		postEvent(h, `{"kind":"view","page":"home","referrer":"`+ref+`"}`, desktopUA, "text/plain", nil)
	}
	for _, r := range stored(t, s, dir) {
		if r.Referrer != "" {
			t.Errorf("referrer %q kept", r.Referrer)
		}
	}
}

func TestBotsAreDropped(t *testing.T) {
	s, h, dir := eventServer(t)
	for _, ua := range []string{"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", "curl/8.5.0", "Mozilla/5.0 HeadlessChrome/120", "AhrefsBot/7.0", ""} {
		if w := postEvent(h, `{"kind":"view","page":"home"}`, ua, "text/plain", nil); w.Code != 204 {
			t.Errorf("%q answered %d", ua, w.Code)
		}
	}
	if rows := stored(t, s, dir); len(rows) != 0 {
		t.Errorf("bot events kept: %+v", rows)
	}
}

func TestDeviceClass(t *testing.T) {
	for ua, want := range map[string]string{
		desktopUA: "desktop",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148":   "mobile",
		"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/120 Mobile Safari/537.36": "mobile",
		"Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15":                          "tablet",
		"Mozilla/5.0 (Linux; Android 13; SM-X700) AppleWebKit/537.36 Chrome/120 Safari/537.36":        "tablet",
	} {
		if got := deviceClass(ua); got != want {
			t.Errorf("%q is %s, want %s", ua, got, want)
		}
	}
}

func TestVisitorStableWithinDayAndChangesAcrossDays(t *testing.T) {
	s, h, dir := eventServer(t)
	day := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	s.events.now = func() time.Time { return day }
	view := `{"kind":"view","page":"home"}`
	postEvent(h, view, desktopUA, "text/plain", nil)
	day = day.Add(5 * time.Hour)
	postEvent(h, view, desktopUA, "text/plain", nil)
	postEvent(h, view, desktopUA+" other", "text/plain", nil)
	oldSalt, err := os.ReadFile(filepath.Join(dir, "salt-2026-03-01"))
	if err != nil || len(oldSalt) != 32 {
		t.Fatalf("salt: %v, %d bytes", err, len(oldSalt))
	}
	day = day.Add(24 * time.Hour)
	postEvent(h, view, desktopUA, "text/plain", nil)
	if _, err := os.Stat(filepath.Join(dir, "salt-2026-03-01")); err == nil {
		t.Error("the previous day's salt was not deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, "salt-2026-03-02")); err != nil {
		t.Errorf("the new salt is missing: %v", err)
	}
	rows := stored(t, s, dir)
	if len(rows) != 4 {
		t.Fatalf("rows %+v", rows)
	}
	if rows[0].Visitor != rows[1].Visitor {
		t.Error("the same visitor differs within a day")
	}
	if rows[0].Visitor == rows[2].Visitor {
		t.Error("another User-Agent gave the same visitor")
	}
	if rows[0].Visitor == rows[3].Visitor || rows[0].Day == rows[3].Day {
		t.Error("the visitor did not change across days")
	}
}

func TestNoAddressOrUserAgentStored(t *testing.T) {
	s, h, dir := eventServer(t)
	ua := "Mozilla/5.0 UniqueAgentMarker/9.9"
	postEvent(h, `{"kind":"view","page":"home","path":"/"}`, ua, "text/plain", map[string]string{"CF-Connecting-IP": "198.51.100.23"})
	postEvent(h, `{"kind":"scroll","page":"home","value":25}`, ua, "text/plain", nil)
	rows := stored(t, s, dir)
	if len(rows) != 2 {
		t.Fatalf("rows %+v", rows)
	}
	for _, r := range rows {
		all := strings.Join([]string{r.At, r.Day, r.Kind, r.Page, r.Path, r.Test, r.Arm, r.Referrer, r.Source, r.Medium, r.Campaign, r.Target, r.Visitor, r.Country, r.Device}, "|")
		for _, bad := range []string{"198.51.100", "203.0.113", "UniqueAgentMarker", "Mozilla"} {
			if strings.Contains(all, bad) {
				t.Errorf("row holds %q: %s", bad, all)
			}
		}
	}
	// Nor the raw files: the database and its log.
	for _, name := range []string{"events.db", "events.db-wal"} {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		for _, bad := range []string{"198.51.100", "203.0.113", "UniqueAgentMarker"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s holds %q", name, bad)
			}
		}
	}
}

func TestSubmitRecordedWithoutEmail(t *testing.T) {
	s, h, dir := eventServer(t)
	form := url.Values{"email": {"someone@example.com"}, "name": {"Some One"}, "github": {"someone"}, "interest": {"use"}, "about": {"hello there"},
		"page": {"home-b"}, "test": {"hero-1"}}
	r := httptest.NewRequest("POST", "/request-access", strings.NewReader(form.Encode()))
	r.Host = "chug.gy"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("User-Agent", desktopUA)
	r.AddCookie(&http.Cookie{Name: cookieName("hero-1"), Value: "home-b"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("the form answered %d", w.Code)
	}
	rows := stored(t, s, dir)
	if len(rows) != 1 || rows[0].Kind != "submit" || rows[0].Page != "home-b" || rows[0].Test != "hero-1" || rows[0].Arm != "home-b" {
		t.Fatalf("rows %+v", rows)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "events.db"))
	w2, _ := os.ReadFile(filepath.Join(dir, "events.db-wal"))
	for _, bad := range []string{"someone", "hello there", "Some One"} {
		if strings.Contains(string(b)+string(w2), bad) {
			t.Errorf("the event store holds %q", bad)
		}
	}
}

func TestTmpdirIsSetUnderDataBeforeOpen(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", "/nonexistent")
	s, err := openEventStore(dir, "chug.gy")
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	want := filepath.Join(dir, "tmp")
	if got := os.Getenv("TMPDIR"); got != want {
		t.Errorf("TMPDIR is %q, want %q", got, want)
	}
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Errorf("%s is not a directory: %v", want, err)
	}
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal mode %q, %v", mode, err)
	}
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != schemaVersion {
		t.Errorf("user_version %d, %v", v, err)
	}
}

func TestEventsWithoutDataDirectory(t *testing.T) {
	keepTmpdir(t)
	s, err := newServer(config{host: "chug.gy"})
	if err != nil {
		t.Fatal(err)
	}
	h := s.handler()
	if w := postEvent(h, `{"kind":"view","page":"home"}`, desktopUA, "text/plain", nil); w.Code != 204 {
		t.Errorf("answered %d", w.Code)
	}
	if w := postEvent(h, `{"kind":"hover","page":"home"}`, desktopUA, "text/plain", nil); w.Code != 400 {
		t.Errorf("a bad event answered %d", w.Code)
	}
}

func TestEventsAreRateLimited(t *testing.T) {
	_, h, _ := eventServer(t)
	var last int
	for i := 0; i < 130; i++ {
		last = postEvent(h, `{"kind":"view","page":"home"}`, desktopUA, "text/plain", nil).Code
	}
	if last != 429 {
		t.Errorf("the 130th event answered %d", last)
	}
}

func TestRobotsDisallowsEvents(t *testing.T) {
	_, h, _ := testServer(t, false)
	if body := get(h, "chug.gy", "/robots.txt").Body.String(); !strings.Contains(body, "Disallow: /e\n") {
		t.Errorf("robots.txt: %q", body)
	}
}

// The script is checked by reading it: no JavaScript runs in the tests.
func TestAnalyticsScriptGuardsAndTransport(t *testing.T) {
	b, err := site.ReadFile("site/analytics.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	guard := strings.Index(js, `root.hasAttribute("data-preview") || navigator.webdriver) return;`)
	first := strings.Index(js, "send(")
	if guard < 0 || first < 0 || guard > first {
		t.Error("the preview and webdriver guard must come before anything is sent")
	}
	for _, want := range []string{"navigator.sendBeacon(\"/e\", body)", "keepalive: true", "JSON.stringify", `kind: "view"`, `kind: "scroll"`, `kind: "engaged"`, `kind: "click"`, `kind: "form_start"`, "document.referrer", "utm_source"} {
		if !strings.Contains(js, want) {
			t.Errorf("analytics.js lacks %s", want)
		}
	}
	for _, bad := range []string{"document.cookie", "localStorage", "sessionStorage", "XMLHttpRequest", "http://", "https://"} {
		if strings.Contains(js, bad) {
			t.Errorf("analytics.js mentions %s", bad)
		}
	}
	_, h, _ := testServer(t, false)
	if w := get(h, "chug.gy", "/analytics.js"); w.Code != 200 {
		t.Errorf("/analytics.js answered %d", w.Code)
	}
	if body := get(h, "chug.gy", "/").Body.String(); !strings.Contains(body, `src="/analytics.js"`) {
		t.Error("the home page does not load the script")
	}
}

// Opening the store sets TMPDIR to a directory that goes away with the test;
// this has the test put the old value back when it ends.
func keepTmpdir(t *testing.T) { t.Setenv("TMPDIR", os.Getenv("TMPDIR")) }

func testEvent(i int) event {
	return event{eventIn: eventIn{Kind: "view", Page: "home", Value: 0, Path: fmt.Sprint("/", i)},
		At: time.Unix(int64(i), 0).UTC(), Day: "2026-01-01", Visitor: "v"}
}

func countRows(t *testing.T, dir string) int {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWriterWritesABatchWholeAndCloseFlushes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	s, err := openEventStore(dir, "chug.gy")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		s.queue <- testEvent(i)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, dir); n != 1000 {
		t.Errorf("%d events kept, want 1000", n)
	}
}

func TestFailedBatchDoesNotStopWriter(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	s, err := openEventStore(dir, "chug.gy")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.writeBatch([]event{testEvent(1), testEvent(2)}); err != nil {
		t.Fatal(err)
	}
	// A trigger that refuses one event makes its whole batch fail.
	if _, err := s.db.Exec(`CREATE TRIGGER refuse BEFORE INSERT ON events WHEN NEW.path = '/bad' BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	bad := testEvent(3)
	bad.Path = "/bad"
	if s.writeBatch([]event{testEvent(4), bad}) == nil {
		t.Fatal("the failing batch succeeded")
	}
	s.queue <- bad
	for len(s.queue) > 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond) // the writer has taken it and failed
	s.queue <- testEvent(5)
	s.queue <- testEvent(6)
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	// 1 and 2 from the first batch; 4 rolled back with its failed batch; 5 and
	// 6 after the failure.
	if n := countRows(t, dir); n != 4 {
		t.Errorf("%d events kept, want 4", n)
	}
}
