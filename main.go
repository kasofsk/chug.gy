// chug.gy: chuggy's public site, and the form that asks for an invite.
//
// One static binary serves the pages embedded in it, takes a request for
// access as one JSON line appended to requests.jsonl, and serves that file back
// to whoever holds the admin token or signs in to the dashboard at /admin. It
// also measures itself: the pages' own script posts events (views, scrolling,
// time on the page, clicks, the form) to POST /e, and they are kept in SQLite
// at events.db beside requests.jsonl, with a visitor ID that changes daily and
// no address or User-Agent. Nothing else is stored. It makes three kinds of outbound call, all plain HTTP
// inside the cluster: to chuggy's Hydra, to redeem a sign-in code and read the
// keys that sign the id_token, and to chuggy's Keto, to ask whether the person
// may open the dashboard.
//
// Configuration is the environment:
//
//	CHUG_SITE_LISTEN            the address to listen on, default :8080
//	CHUG_SITE_DATA              the directory requests.jsonl, events.db, the
//	                            daily salt and tmp/ are kept in; unset, the form
//	                            answers 503 and says so and /e keeps nothing
//	CHUG_SITE_ADMIN_TOKEN_FILE  a file holding the bearer token that reads the
//	                            requests back; unset, the route is 404
//	CHUG_SITE_HOST              the canonical host; a request for another host
//	                            is redirected to it, except on loopback and for
//	                            /healthz
//	CHUG_SITE_DOCS              a checkout of kasofsk/chuggy-docs, rendered at
//	                            /docs; unset, /docs and /llms.txt go to GitHub
//
// The dashboard at /admin is off (every page 404) until all of these are set,
// CHUG_SITE_HOST among them, and both files exist and are long enough. The
// files are read when needed, not at start.
//
//	CHUG_SITE_OIDC_ISSUER              the issuer, https://auth.vteng.io; the
//	                                   browser is sent to <issuer>/oauth2/auth
//	CHUG_SITE_OIDC_INTERNAL_URL        the same Hydra over the pod network; the
//	                                   code is redeemed at <it>/oauth2/token and
//	                                   keys are read from <it>/.well-known/jwks.json
//	CHUG_SITE_OIDC_CLIENT_ID           the Hydra client, chuggy-chug-gy
//	CHUG_SITE_OIDC_CLIENT_SECRET_FILE  a file holding that client's secret
//	CHUG_SITE_SESSION_KEY_FILE         a file holding at least 32 bytes that sign
//	                                   the cookies
//	CHUG_SITE_KETO_READ_URL            Keto's read API, which is asked whether the
//	                                   person holds Site:main#read_marketing
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/mail"
	"os"
	"os/signal"
	"path"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed site
var site embed.FS

// The pages, read once. The pages of pages.go are loaded with the server.
var (
	requestPage []byte
	styles      []byte
	stylesKey   string
)

func init() {
	must := func(name string) []byte {
		b, err := site.ReadFile("site/" + name)
		if err != nil {
			panic(err)
		}
		return b
	}
	styles = must("site.css")
	sum := sha256.Sum256(styles)
	stylesKey = hex.EncodeToString(sum[:8])
	requestPage = must("requested.html")
}

// Redirects: short paths on this host that stand for pages elsewhere, so a
// link printed anywhere can say chug.gy/linux and keep working when the
// runner's page moves. /docs and /llms.txt are among them only when the docs
// are not rendered here.
var redirects = map[string]string{
	"/app":     "https://chuggy.vteng.io/",
	"/sign-in": "https://chuggy.vteng.io/",
	"/github":  "https://github.com/kasofsk",
	"/linux":   "https://github.com/kasofsk/chuggy-linux#readme",
	"/mac":     "https://github.com/kasofsk/chuggy-darwin#readme",
	"/windows": "https://github.com/kasofsk/chuggy-windows#readme",
}

var docsRedirects = map[string]string{
	"/docs":     "https://github.com/kasofsk/chuggy-docs#readme",
	"/llms.txt": "https://raw.githubusercontent.com/kasofsk/chuggy-docs/main/llms.txt",
}

type config struct {
	listen         string
	dataDir        string
	adminTokenFile string
	host           string
	docsDir        string
	admin          adminConfig
}

func configFromEnv() config {
	c := config{
		listen:         os.Getenv("CHUG_SITE_LISTEN"),
		dataDir:        os.Getenv("CHUG_SITE_DATA"),
		adminTokenFile: os.Getenv("CHUG_SITE_ADMIN_TOKEN_FILE"),
		host:           os.Getenv("CHUG_SITE_HOST"),
		docsDir:        os.Getenv("CHUG_SITE_DOCS"),
		admin: adminConfig{
			issuer:         os.Getenv("CHUG_SITE_OIDC_ISSUER"),
			internalURL:    os.Getenv("CHUG_SITE_OIDC_INTERNAL_URL"),
			clientID:       os.Getenv("CHUG_SITE_OIDC_CLIENT_ID"),
			secretFile:     os.Getenv("CHUG_SITE_OIDC_CLIENT_SECRET_FILE"),
			sessionKeyFile: os.Getenv("CHUG_SITE_SESSION_KEY_FILE"),
			ketoURL:        os.Getenv("CHUG_SITE_KETO_READ_URL"),
		},
	}
	if c.listen == "" {
		c.listen = ":8080"
	}
	return c
}

// A request for access, as the form sends it and the file keeps it.
type accessRequest struct {
	At       time.Time `json:"at"`
	Email    string    `json:"email"`
	Name     string    `json:"name,omitempty"`
	GitHub   string    `json:"github,omitempty"`
	Interest string    `json:"interest"`
	About    string    `json:"about,omitempty"`
	Page     string    `json:"page,omitempty"`
	Test     string    `json:"test,omitempty"`
	Arm      string    `json:"arm,omitempty"`
}

var (
	githubLogin = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
	interests   = map[string]bool{"use": true, "partner": true, "invest": true, "other": true}
)

const (
	maxFormBytes = 16 << 10
	maxAbout     = 2000
	maxName      = 200
)

// Reads a request off the form, or says which field is wrong.
func parseAccessRequest(r *http.Request) (accessRequest, error) {
	if err := r.ParseForm(); err != nil {
		return accessRequest{}, errors.New("the form could not be read")
	}
	field := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }
	if field("website") != "" {
		// The honeypot: a field no person sees, which only a script fills.
		return accessRequest{}, errors.New("the form could not be read")
	}
	req := accessRequest{
		At:       time.Now().UTC().Truncate(time.Second),
		Email:    field("email"),
		Name:     field("name"),
		GitHub:   strings.TrimPrefix(field("github"), "@"),
		Interest: field("interest"),
		About:    field("about"),
	}
	addr, err := mail.ParseAddress(req.Email)
	if err != nil || addr.Name != "" || len(req.Email) > 254 {
		return req, errors.New("enter an email address")
	}
	req.Email = addr.Address
	if len(req.Name) > maxName {
		return req, errors.New("the name is too long")
	}
	if req.GitHub != "" && (!githubLogin.MatchString(req.GitHub) || strings.Contains(req.GitHub, "--")) {
		return req, errors.New("enter a GitHub username, such as octocat")
	}
	if !interests[req.Interest] {
		return req, errors.New("say what brings you here")
	}
	if len(req.About) > maxAbout {
		return req, fmt.Errorf("keep the note under %d characters", maxAbout)
	}
	if strings.ContainsAny(req.Email+req.Name+req.GitHub, "\r\n") {
		return req, errors.New("the form could not be read")
	}
	return req, nil
}

// Appends requests to one file, one line each, under one lock.
type requestStore struct {
	path string
	mu   sync.Mutex
}

func (s *requestStore) append(req accessRequest) error {
	line, err := json.Marshal(req)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func (s *requestStore) open() (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return io.NopCloser(strings.NewReader("")), nil
	}
	return f, err
}

// A small per-address limit on the form, so a script cannot fill the disk.
// Times are kept as nanoseconds: an address costs a third of what it would as
// time.Time, which is what a burst of distinct addresses fills memory with.
type limiter struct {
	mu      sync.Mutex
	seen    map[string][]int64
	window  time.Duration
	allowed int
	now     func() time.Time
	swept   int64 // when stale keys were last dropped
}

func newLimiter(allowed int, window time.Duration) *limiter {
	return &limiter{seen: map[string][]int64{}, window: window, allowed: allowed, now: time.Now}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now().UnixNano()
	w := int64(l.window)
	// Stale keys go once a window, not on every call: each key is added by one
	// call, so the sweep's cost is amortised over the calls that made the keys.
	if now-l.swept >= w {
		for k, times := range l.seen {
			if len(times) == 0 || now-times[len(times)-1] >= w {
				delete(l.seen, k)
			}
		}
		l.swept = now
	}
	kept := l.seen[key][:0]
	for _, t := range l.seen[key] {
		if now-t < w {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.allowed {
		l.seen[key] = kept
		return false
	}
	l.seen[key] = append(kept, now)
	return true
}

// The address a request came from: Cloudflare's header at the edge, else the
// connection's.
func clientAddress(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type server struct {
	cfg        config
	store      *requestStore
	events     *eventStore
	eventLimit *limiter
	limit      *limiter
	inflight   chan struct{} // the semaphore shed uses; nil means no cap
	docs       *docs
	reg        *registry
	auth       *adminAuth
	started    time.Time
}

func newServer(cfg config) (*server, error) {
	files, err := fs.Sub(site, "site")
	if err != nil {
		return nil, err
	}
	return newServerWith(cfg, files, pageDefs, slotDefs, testDefs)
}

func newServerWith(cfg config, files fs.FS, pages []pageDef, slots []slotDef, tests []testDef) (*server, error) {
	reg, err := newRegistry(files, pages, slots, tests)
	if err != nil {
		return nil, fmt.Errorf("the pages: %w", err)
	}
	s := &server{cfg: cfg, reg: reg, limit: newLimiter(5, 10*time.Minute), eventLimit: newLimiter(120, time.Minute), auth: newAdminAuth(), started: time.Now(), inflight: make(chan struct{}, maxInFlight)}
	if cfg.dataDir != "" {
		if err := os.MkdirAll(cfg.dataDir, 0o700); err != nil {
			return nil, fmt.Errorf("the data directory: %w", err)
		}
		s.store = &requestStore{path: path.Join(cfg.dataDir, "requests.jsonl")}
		if s.events, err = openEventStore(cfg.dataDir, s.host()); err != nil {
			return nil, fmt.Errorf("the events database: %w", err)
		}
	}
	if cfg.docsDir != "" {
		host := cfg.host
		if host == "" {
			host = "chug.gy"
		}
		d, err := loadDocs(cfg.docsDir, host)
		if err != nil {
			return nil, fmt.Errorf("the docs: %w", err)
		}
		s.docs = d
	}
	return s, nil
}

// The admin token, read when it is asked for rather than at start: the file
// is a Secret the cluster mounts, which can be created after the pod is, and a
// pod without one serves the site with that route off.
func (s *server) adminToken() []byte {
	if s.cfg.adminTokenFile == "" {
		return nil
	}
	b, err := os.ReadFile(s.cfg.adminTokenFile)
	if err != nil {
		return nil
	}
	token := bytes.TrimSpace(b)
	if len(token) < 16 {
		return nil
	}
	return token
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	assets, err := fs.Sub(site, "site")
	if err != nil {
		panic(err)
	}
	s.mountPages(mux)
	mux.HandleFunc("GET /requested", s.requested)
	mux.HandleFunc("POST /request-access", s.requestAccess)
	mux.HandleFunc("POST /e", s.postEvent)
	mux.HandleFunc("GET /admin/requests", s.adminRequests)
	s.mountAdmin(mux)
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /site.css", s.stylesheet)
	mux.Handle("GET /site.js", cacheFor(5*time.Minute, http.FileServerFS(assets)))
	mux.Handle("GET /analytics.js", cacheFor(5*time.Minute, http.FileServerFS(assets)))
	for from, to := range redirects {
		mux.Handle("GET "+from, http.RedirectHandler(to, http.StatusFound))
	}
	if s.docs != nil {
		mux.Handle("GET /docs", s.docsHandler())
		mux.Handle("GET /docs/", s.docsHandler())
		mux.HandleFunc("GET /llms.txt", s.llmsHandler)
	} else {
		for from, to := range docsRedirects {
			mux.Handle("GET "+from, http.RedirectHandler(to, http.StatusFound))
		}
	}
	mux.Handle("GET /fonts/", cacheFor(365*24*time.Hour, http.FileServerFS(assets)))
	mux.Handle("GET /img/", cacheFor(24*time.Hour, http.FileServerFS(assets)))
	mux.Handle("GET /favicon.svg", cacheFor(24*time.Hour, http.FileServerFS(assets)))
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "User-agent: *\nAllow: /\nDisallow: /admin/\nDisallow: /e\n")
	})
	return s.canonicalHost(securityHeaders(s.shed(mux)))
}

// The most requests served at once. Each costs about 30 KiB while it runs (the
// goroutine, the connection's buffers, the page being built); 256 of them is
// well under 10 MiB of the 96 MiB GOMEMLIMIT, which leaves the rest for the
// database, the docs and the garbage collector's headroom.
const maxInFlight = 256

// How long a page waits for a place before it is turned away.
const shedWait = 2 * time.Second

// Bounds the work in flight with a semaphore. Past the cap a page waits a
// moment and then gets 503 with Retry-After; an event is refused at once, its
// body unread. /healthz is never held.
func (s *server) shed(next http.Handler) http.Handler {
	if s.inflight == nil {
		return next
	}
	busy := func(w http.ResponseWriter) {
		w.Header().Set("Retry-After", "5")
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		select {
		case s.inflight <- struct{}{}:
		default:
			if r.URL.Path == "/e" {
				busy(w)
				return
			}
			t := time.NewTimer(shedWait)
			defer t.Stop()
			select {
			case s.inflight <- struct{}{}:
			case <-t.C:
				busy(w)
				return
			case <-r.Context().Done():
				return
			}
		}
		defer func() { <-s.inflight }()
		next.ServeHTTP(w, r)
	})
}

// Every answer carries the same document policy: the pages load only what the
// binary serves, run only their own script, and submit only to themselves,
// whether the form posts or the script sends it.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; font-src 'self'; img-src 'self'; script-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func cacheFor(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(d.Seconds())))
		next.ServeHTTP(w, r)
	})
}

// A request for www.chug.gy, or any other name the edge forwards, is sent to
// the one host the site is at. Loopback is left alone so a local run works,
// and the health route so the kubelet's probe, which names the pod, does.
func (s *server) canonicalHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if s.cfg.host != "" && host != s.cfg.host && r.URL.Path != "/healthz" && !isLoopback(host) {
			target := "https://" + s.cfg.host + r.URL.RequestURI()
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *server) requested(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(bytes.ReplaceAll(requestPage, []byte("{{.StylesKey}}"), []byte(stylesKey)))
}

func (s *server) stylesheet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	if r.URL.Query().Get("v") == stylesKey {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
	w.Write(styles)
}

func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, "ok\n")
}

// Answers the form. A browser with the page's script sends it as a fetch and
// reads the JSON; one without is redirected to the page that says the same.
func (s *server) requestAccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	wantsJSON := strings.Contains(r.Header.Get("Accept"), "application/json")
	answer := func(status int, message string) {
		if wantsJSON {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]string{"message": message})
			return
		}
		if status == http.StatusOK {
			http.Redirect(w, r, "/requested", http.StatusSeeOther)
			return
		}
		http.Error(w, message, status)
	}
	if s.store == nil {
		answer(http.StatusServiceUnavailable, "requests are not being taken right now")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	req, err := parseAccessRequest(r)
	if err != nil {
		answer(http.StatusBadRequest, err.Error())
		return
	}
	if !s.limit.allow(clientAddress(r)) {
		answer(http.StatusTooManyRequests, "that is enough requests for now; try again later")
		return
	}
	req.Page, req.Test, req.Arm = s.origin(r)
	if err := s.store.append(req); err != nil {
		log.Printf("a request could not be kept: %v", err)
		answer(http.StatusInternalServerError, "the request could not be kept; try again in a moment")
		return
	}
	log.Printf("request for access: %s (%s)", req.Email, req.Interest)
	s.recordSubmit(r, req.Page, req.Test, req.Arm)
	answer(http.StatusOK, "received")
}

// Serves the file as it stands, to the holder of the token and nobody else.
func (s *server) adminRequests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	token := s.adminToken()
	if token == nil || s.store == nil {
		http.NotFound(w, r)
		return
	}
	given, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(given)), token) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="chug.gy"`)
		http.Error(w, "a token is needed", http.StatusUnauthorized)
		return
	}
	f, err := s.store.open()
	if err != nil {
		http.Error(w, "the requests could not be read", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	bufio.NewReader(f).WriteTo(w)
}

func main() {
	cfg := configFromEnv()
	s, err := newServer(cfg)
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{
		Addr:              cfg.listen,
		Handler:           s.handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Printf("chug.gy listening on %s", cfg.listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
