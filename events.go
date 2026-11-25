package main

// First-party analytics: the page's own script posts events to POST /e, and
// they are kept in SQLite at <CHUG_SITE_DATA>/events.db. A visitor is counted
// the way Plausible publishes: SHA-256 over a daily salt, the host, the
// address and the User-Agent. Neither the address nor the User-Agent is kept.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	maxEventBytes = 4 << 10
	maxEventStr   = 200
	maxEventPath  = 500
	schemaVersion = 2
)

var eventKinds = map[string]bool{
	"view": true, "scroll": true, "engaged": true, "click": true, "form_start": true, "submit": true,
}

// What the script sends. The server adds the rest.
type eventIn struct {
	Kind        string `json:"kind"`
	Page        string `json:"page"`
	Path        string `json:"path"`
	Test        string `json:"test"`
	Arm         string `json:"arm"`
	Referrer    string `json:"referrer"`
	UTMSource   string `json:"utm_source"`
	UTMMedium   string `json:"utm_medium"`
	UTMCampaign string `json:"utm_campaign"`
	Target      string `json:"target"` // the data-track name of a click
	Value       int    `json:"value"`  // scroll percent or engaged seconds
}

// An event as stored.
type event struct {
	eventIn
	At      time.Time
	Day     string
	Visitor string
	Country string
	Device  string
}

var (
	botPattern    = regexp.MustCompile(`(?i)bot\b|bot/|crawl|spider|slurp|facebookexternalhit|embedly|preview|headless|lighthouse|monitor|uptime|curl/|wget|python-requests|go-http-client|httpclient|libwww|scrapy|java/|okhttp|pingdom|phantomjs|archive\.org|feedfetcher|mediapartners|bingpreview|yandex|baidu`)
	tabletPattern = regexp.MustCompile(`(?i)ipad|tablet|kindle|silk/|playbook`)
	mobilePattern = regexp.MustCompile(`(?i)mobi|iphone|ipod|android|windows phone|blackberry|opera mini`)
	countryCode   = regexp.MustCompile(`^[A-Z]{2}$`)
)

func isBot(ua string) bool { return ua == "" || botPattern.MatchString(ua) }

// Android without "Mobile" in its User-Agent is a tablet.
func androidTablet(ua string) bool {
	l := strings.ToLower(ua)
	return strings.Contains(l, "android") && !strings.Contains(l, "mobile")
}

func deviceClass(ua string) string {
	switch {
	case tabletPattern.MatchString(ua) || androidTablet(ua):
		return "tablet"
	case mobilePattern.MatchString(ua):
		return "mobile"
	}
	return "desktop"
}

type eventStore struct {
	db      *sql.DB // the writer's
	read    *sql.DB // the dashboard's: a connection of its own, so a slow query never holds up a write
	dir     string
	host    string
	mu      sync.Mutex // guards the salt
	saltDay string
	salt    []byte
	queue   chan event
	done    chan struct{}
	now     func() time.Time
}

// Makes <dir>/tmp and points TMPDIR at it before the database opens: SQLite
// sorts and indexes in a temp directory, and production has none.
func openEventStore(dir, host string) (*eventStore, error) {
	tmp := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return nil, err
	}
	if err := os.Setenv("TMPDIR", tmp); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "events.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := migrateEvents(db); err != nil {
		db.Close()
		return nil, err
	}
	read, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "events.db")+"?_pragma=busy_timeout(5000)&_pragma=query_only(1)")
	if err != nil {
		db.Close()
		return nil, err
	}
	read.SetMaxOpenConns(1)
	s := &eventStore{db: db, read: read, dir: dir, host: host, queue: make(chan event, 1024), done: make(chan struct{}), now: time.Now}
	go s.writer()
	return s, nil
}

func migrateEvents(db *sql.DB) error {
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v > schemaVersion {
		return fmt.Errorf("events.db is schema version %d; this binary knows %d", v, schemaVersion)
	}
	// Each step takes the schema to the next version, all or nothing.
	steps := [][]string{
		{
			`CREATE TABLE events (
				id INTEGER PRIMARY KEY,
				at TEXT NOT NULL,
				day TEXT NOT NULL,
				kind TEXT NOT NULL,
				page TEXT NOT NULL,
				path TEXT NOT NULL DEFAULT '',
				test TEXT NOT NULL DEFAULT '',
				arm TEXT NOT NULL DEFAULT '',
				referrer TEXT NOT NULL DEFAULT '',
				utm_source TEXT NOT NULL DEFAULT '',
				utm_medium TEXT NOT NULL DEFAULT '',
				utm_campaign TEXT NOT NULL DEFAULT '',
				target TEXT NOT NULL DEFAULT '',
				value INTEGER NOT NULL DEFAULT 0,
				visitor TEXT NOT NULL,
				country TEXT NOT NULL DEFAULT '',
				device TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE INDEX events_day ON events (day, kind)`,
			`CREATE INDEX events_test_arm ON events (test, arm, kind, day)`,
		},
		{
			// The Tests page counts visitor-days from this index alone, in its
			// order, so it never sorts or reads the table.
			`DROP INDEX events_test_arm`,
			`CREATE INDEX events_tests ON events (test, arm, day, visitor, kind, value) WHERE test != ''`,
		},
	}
	for ; v < len(steps); v++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		for _, q := range append(steps[v], fmt.Sprintf("PRAGMA user_version = %d", v+1)) {
			if _, err := tx.Exec(q); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

const insertEvent = `INSERT INTO events (at, day, kind, page, path, test, arm, referrer, utm_source, utm_medium, utm_campaign, target, value, visitor, country, device)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const maxBatch = 256

// Writes queued events, so a page's own request never waits on the disk. It
// takes one, then whatever else is waiting up to maxBatch, and keeps the batch
// in one transaction.
func (s *eventStore) writer() {
	defer close(s.done)
	batch := make([]event, 0, maxBatch)
	for e := range s.queue {
		batch = append(batch[:0], e)
	fill:
		for len(batch) < maxBatch {
			select {
			case e, ok := <-s.queue:
				if !ok {
					break fill
				}
				batch = append(batch, e)
			default:
				break fill
			}
		}
		if err := s.writeBatch(batch); err != nil {
			log.Printf("an event could not be kept: %v", err)
		}
	}
}

func (s *eventStore) writeBatch(batch []event) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(insertEvent)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range batch {
		if _, err := stmt.Exec(e.At.Format(time.RFC3339), e.Day, e.Kind, e.Page, e.Path, e.Test, e.Arm,
			e.Referrer, e.UTMSource, e.UTMMedium, e.UTMCampaign, e.Target, e.Value, e.Visitor, e.Country, e.Device); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Waits for queued events to be written, then closes the database.
func (s *eventStore) close() error {
	close(s.queue)
	<-s.done
	s.read.Close()
	return s.db.Close()
}

// The salt for a UTC day: made at the day's first event, kept in the data
// directory, and the other days' salts deleted once it exists.
func (s *eventStore) saltFor(day string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saltDay == day {
		return s.salt, nil
	}
	file := filepath.Join(s.dir, "salt-"+day)
	salt, err := os.ReadFile(file)
	if err != nil || len(salt) != 32 {
		salt = make([]byte, 32)
		if _, err := rand.Read(salt); err != nil {
			return nil, err
		}
		if err := os.WriteFile(file, salt, 0o600); err != nil {
			return nil, err
		}
	}
	old, _ := filepath.Glob(filepath.Join(s.dir, "salt-*"))
	for _, f := range old {
		if f != file {
			os.Remove(f)
		}
	}
	s.saltDay, s.salt = day, salt
	return salt, nil
}

func (s *eventStore) visitor(day, addr, ua string) (string, error) {
	salt, err := s.saltFor(day)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, part := range []string{string(salt), s.host, addr, ua} {
		io.WriteString(h, part)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:16]), nil
}

// Stamps an event with the time, visitor, country and device, and queues it.
func (s *eventStore) record(in eventIn, r *http.Request) {
	now := s.now().UTC()
	day := now.Format("2006-01-02")
	ua := r.UserAgent()
	v, err := s.visitor(day, clientAddress(r), ua)
	if err != nil {
		log.Printf("an event could not be kept: %v", err)
		return
	}
	country := strings.ToUpper(strings.TrimSpace(r.Header.Get("CF-IPCountry")))
	if !countryCode.MatchString(country) || country == "XX" || country == "T1" {
		country = ""
	}
	e := event{eventIn: in, At: now.Truncate(time.Second), Day: day, Visitor: v, Country: country, Device: deviceClass(ua)}
	select {
	case s.queue <- e:
	default:
		log.Printf("an event was dropped: the queue is full")
	}
}

// Reduces a referrer to its host; empty when it is not a URL or is this site.
func referrerHost(ref, own string) string {
	u, err := url.Parse(ref)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	if h == own || h == "www."+own {
		return ""
	}
	return h
}

func okString(s string, max int) bool {
	if len(s) > max {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Checks an event against the registry, or says what is wrong.
func (s *server) validateEvent(in *eventIn) error {
	if !eventKinds[in.Kind] || in.Kind == "submit" {
		return errors.New("unknown kind")
	}
	if s.reg.pages[in.Page] == nil {
		return errors.New("unknown page")
	}
	if (in.Test == "") != (in.Arm == "") {
		return errors.New("test and arm go together")
	}
	if in.Test != "" {
		t, ok := s.reg.tests[in.Test]
		if !ok || !t.hasArm(in.Arm) {
			return errors.New("unknown test and arm")
		}
	}
	for _, f := range []struct {
		v   string
		max int
	}{{in.Path, maxEventPath}, {in.Referrer, 2000}, {in.UTMSource, maxEventStr}, {in.UTMMedium, maxEventStr},
		{in.UTMCampaign, maxEventStr}, {in.Target, maxEventStr}} {
		if !okString(f.v, f.max) {
			return errors.New("a field is too long or unreadable")
		}
	}
	if in.Path != "" && !strings.HasPrefix(in.Path, "/") {
		return errors.New("bad path")
	}
	switch in.Kind {
	case "scroll":
		if in.Value != 25 && in.Value != 50 && in.Value != 75 && in.Value != 100 {
			return errors.New("bad scroll value")
		}
	case "engaged":
		if in.Value < 1 || in.Value > 86400 {
			return errors.New("bad engaged value")
		}
	case "click":
		if in.Target == "" {
			return errors.New("a click needs a target")
		}
		if in.Value != 0 {
			return errors.New("bad value")
		}
	default:
		if in.Value != 0 {
			return errors.New("bad value")
		}
	}
	if in.Kind != "click" && in.Target != "" {
		return errors.New("bad target")
	}
	if in.Kind != "view" && (in.Referrer != "" || in.UTMSource != "" || in.UTMMedium != "" || in.UTMCampaign != "") {
		return errors.New("bad field")
	}
	in.Referrer = referrerHost(in.Referrer, s.host())
	return nil
}

func (s *server) host() string {
	if s.cfg.host == "" {
		return "chug.gy"
	}
	return s.cfg.host
}

// POST /e: one event, as JSON in whatever content type the beacon used.
func (s *server) postEvent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.eventLimit.allow(clientAddress(r)) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEventBytes))
	if err != nil {
		http.Error(w, "too large", http.StatusBadRequest)
		return
	}
	var in eventIn
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || dec.More() {
		http.Error(w, "not an event", http.StatusBadRequest)
		return
	}
	if err := s.validateEvent(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.events != nil && !isBot(r.UserAgent()) {
		s.events.record(in, r)
	}
	w.WriteHeader(http.StatusNoContent)
}

// Records a successful form submission: the page, test and arm and nothing
// the person typed.
func (s *server) recordSubmit(r *http.Request, page, test, armName string) {
	if s.events == nil || page == "" || isBot(r.UserAgent()) {
		return
	}
	s.events.record(eventIn{Kind: "submit", Page: page, Test: test, Arm: armName}, r)
}
