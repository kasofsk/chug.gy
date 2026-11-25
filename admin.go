package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// The dashboard at /admin. A person signs in with their chuggy account at
// Hydra, and the site asks Keto whether they hold Site:main#read_marketing
// before it shows anything. GET /admin/requests, the bearer route, is
// separate and never comes through here.

type adminConfig struct {
	issuer         string
	internalURL    string
	clientID       string
	secretFile     string
	sessionKeyFile string
	ketoURL        string
}

const (
	sessionLife = 12 * time.Hour
	flowLife    = 10 * time.Minute
	ketoTTL     = 60 * time.Second

	sessionCookie = "chug_admin"
	flowCookie    = "chug_admin_flow"
)

// What the dashboard needs, resolved when asked for: the files may appear
// after the pod does.
type adminCreds struct {
	secret, key []byte
}

func readSecret(file string, min int) []byte {
	if file == "" {
		return nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	b = bytes.TrimSpace(b)
	if len(b) < min {
		return nil
	}
	return b
}

// The credentials, or nil while any setting is missing: then every admin page
// is a 404.
func (s *server) adminCreds() *adminCreds {
	c := s.cfg.admin
	if s.cfg.host == "" || c.issuer == "" || c.internalURL == "" || c.clientID == "" || c.ketoURL == "" {
		return nil
	}
	secret, key := readSecret(c.secretFile, 1), readSecret(c.sessionKeyFile, 32)
	if secret == nil || key == nil {
		return nil
	}
	return &adminCreds{secret: secret, key: key}
}

// The state the dashboard keeps between requests: the clients it calls out
// with, the verifier, and what Keto last said of each person.
type adminAuth struct {
	client *http.Client
	now    func() time.Time

	once     sync.Once
	verifier *oidc.IDTokenVerifier

	mu    sync.Mutex
	cache map[string]ketoAnswer
}

type ketoAnswer struct {
	allowed bool
	until   time.Time
}

func newAdminAuth() *adminAuth {
	return &adminAuth{
		now: time.Now,
		client: &http.Client{
			Timeout: 10 * time.Second,
			// A redirect would carry the client secret somewhere unnamed.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		cache: map[string]ketoAnswer{},
	}
}

func (s *server) idVerifier() *oidc.IDTokenVerifier {
	a := s.auth
	a.once.Do(func() {
		c := s.cfg.admin
		ctx := oidc.ClientContext(context.Background(), a.client)
		keys := oidc.NewRemoteKeySet(ctx, strings.TrimRight(c.internalURL, "/")+"/.well-known/jwks.json")
		a.verifier = oidc.NewVerifier(c.issuer, keys, &oidc.Config{ClientID: c.clientID, Now: a.now})
	})
	return a.verifier
}

// ---------------------------------------------------------------- cookies --

// A cookie holds a JSON payload and an HMAC-SHA256 of it under the session
// key, bound to a kind so one cookie cannot stand in for another.
func seal(key []byte, kind string, v any) string {
	payload, _ := json.Marshal(v)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(kind + "\x00"))
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func unseal(key []byte, kind, value string, v any) bool {
	p, m, ok := strings.Cut(value, ".")
	if !ok {
		return false
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(p)
	sum, err2 := base64.RawURLEncoding.DecodeString(m)
	if err1 != nil || err2 != nil {
		return false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(kind + "\x00"))
	mac.Write(payload)
	if !hmac.Equal(sum, mac.Sum(nil)) {
		return false
	}
	return json.Unmarshal(payload, v) == nil
}

type sessionClaims struct {
	Principal string `json:"p"`
	Expires   int64  `json:"exp"`
}

type flowClaims struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	Return   string `json:"return"`
	Expires  int64  `json:"exp"`
}

func requestHost(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		return h
	}
	return r.Host
}

func (s *server) setCookie(w http.ResponseWriter, r *http.Request, name, value, path string, life time.Duration) {
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   !isLoopback(requestHost(r)),
	}
	if life > 0 {
		c.MaxAge = int(life.Seconds())
	} else {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

// The person a valid session names, or "".
func (s *server) sessionPrincipal(r *http.Request, key []byte) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	var claims sessionClaims
	if !unseal(key, "session", c.Value, &claims) || claims.Principal == "" || s.auth.now().Unix() >= claims.Expires {
		return ""
	}
	return claims.Principal
}

// ------------------------------------------------------------------ paths --

// A page to return to after sign-in must be an admin page of this site: a
// path, not a URL, and not one of the routes that make the flow.
func safeReturn(p string) string {
	const fallback = "/admin"
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || strings.ContainsAny(p, "\\\r\n") {
		return fallback
	}
	if u.Path != "/admin" && !strings.HasPrefix(u.Path, "/admin/") {
		return fallback
	}
	switch u.Path {
	case "/admin/callback", "/admin/sign-out", "/admin/requests":
		return fallback
	}
	if strings.Contains(u.Path, "//") || strings.Contains(u.Path, "/../") || strings.HasSuffix(u.Path, "/..") {
		return fallback
	}
	return p
}

// Where Hydra sends the browser back to: the site's host, or on loopback the
// host the request came to.
func (s *server) redirectURI(r *http.Request) string {
	if isLoopback(requestHost(r)) {
		return "http://" + r.Host + "/admin/callback"
	}
	return "https://" + s.cfg.host + "/admin/callback"
}

func (s *server) ownOrigin(r *http.Request) string {
	if isLoopback(requestHost(r)) {
		return "http://" + r.Host
	}
	return "https://" + s.cfg.host
}

func (s *server) oauthConfig(r *http.Request, c *adminCreds) *oauth2.Config {
	a := s.cfg.admin
	return &oauth2.Config{
		ClientID:     a.clientID,
		ClientSecret: string(c.secret),
		Endpoint: oauth2.Endpoint{
			AuthURL:   strings.TrimRight(a.issuer, "/") + "/oauth2/auth",
			TokenURL:  strings.TrimRight(a.internalURL, "/") + "/oauth2/token",
			AuthStyle: oauth2.AuthStyleInHeader,
		},
		RedirectURL: s.redirectURI(r),
		Scopes:      []string{"openid"},
	}
}

func randomToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// ------------------------------------------------------------------- keto --

// Asks Keto whether the principal may read the marketing pages. Only a clean
// 200 with a boolean is an answer; anything else is an error.
func (s *server) ketoAllows(ctx context.Context, principal string) (bool, error) {
	base, err := url.Parse(strings.TrimRight(s.cfg.admin.ketoURL, "/"))
	if err != nil {
		return false, err
	}
	u := base.JoinPath("relation-tuples", "check", "openapi")
	u.RawQuery = url.Values{
		"namespace":  {"Site"},
		"object":     {"main"},
		"relation":   {"read_marketing"},
		"subject_id": {principal},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return false, err
	}
	resp, err := s.auth.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("keto answered %d", resp.StatusCode)
	}
	var answer struct {
		Allowed *bool `json:"allowed"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || answer.Allowed == nil {
		return false, errors.New("keto gave no clear answer")
	}
	return *answer.Allowed, nil
}

// The same, remembered for a minute per person unless fresh is set.
func (s *server) permitted(ctx context.Context, principal string, fresh bool) (bool, error) {
	a := s.auth
	now := a.now()
	if !fresh {
		a.mu.Lock()
		got, ok := a.cache[principal]
		a.mu.Unlock()
		if ok && now.Before(got.until) {
			return got.allowed, nil
		}
	}
	allowed, err := s.ketoAllows(ctx, principal)
	if err != nil {
		return false, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.cache) > 1000 {
		for k, v := range a.cache {
			if !now.Before(v.until) {
				delete(a.cache, k)
			}
		}
	}
	a.cache[principal] = ketoAnswer{allowed: allowed, until: now.Add(ketoTTL)}
	return allowed, nil
}

// --------------------------------------------------------------- handlers --

func adminHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
}

// Wraps a route of the dashboard: 404 while it is not configured, and for
// the pages, a person who is signed in and still permitted.
func (s *server) admin(needSession bool, next func(http.ResponseWriter, *http.Request, *adminCreds, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		adminHeaders(w)
		creds := s.adminCreds()
		if creds == nil {
			http.NotFound(w, r)
			return
		}
		if !needSession {
			next(w, r, creds, "")
			return
		}
		principal := s.sessionPrincipal(r, creds.key)
		if principal == "" {
			s.beginSignIn(w, r, creds)
			return
		}
		ok, err := s.permitted(r.Context(), principal, false)
		if err != nil {
			log.Printf("admin: keto: %v", err)
			s.errorPage(w, http.StatusServiceUnavailable, "Unavailable", "The permission service could not be reached. Try again in a moment.", true)
			return
		}
		if !ok {
			s.errorPage(w, http.StatusForbidden, "Refused", "Your account can't open the dashboard.", false)
			return
		}
		next(w, r, creds, principal)
	}
}

func (s *server) beginSignIn(w http.ResponseWriter, r *http.Request, c *adminCreds) {
	flow := flowClaims{
		State:    randomToken(),
		Nonce:    randomToken(),
		Verifier: oauth2.GenerateVerifier(),
		Return:   safeReturn(r.URL.RequestURI()),
		Expires:  s.auth.now().Add(flowLife).Unix(),
	}
	s.setCookie(w, r, flowCookie, seal(c.key, "flow", flow), "/admin", flowLife)
	target := s.oauthConfig(r, c).AuthCodeURL(flow.State,
		oauth2.S256ChallengeOption(flow.Verifier),
		oauth2.SetAuthURLParam("nonce", flow.Nonce))
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *server) callback(w http.ResponseWriter, r *http.Request, c *adminCreds, _ string) {
	// The flow ends here whatever happens: its cookie is spent.
	s.setCookie(w, r, flowCookie, "", "/admin", 0)
	fail := func(status int, title, message string) {
		s.errorPage(w, status, title, message, true)
	}
	var flow flowClaims
	cookie, err := r.Cookie(flowCookie)
	if err != nil || !unseal(c.key, "flow", cookie.Value, &flow) || s.auth.now().Unix() >= flow.Expires {
		fail(http.StatusBadRequest, "Expired", "The sign-in took too long or did not start here. Start again.")
		return
	}
	q := r.URL.Query()
	if flow.State == "" || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(flow.State)) != 1 {
		fail(http.StatusBadRequest, "Refused", "The sign-in did not match its start. Start again.")
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		fail(http.StatusBadRequest, "Refused", "Sign-in did not complete.")
		return
	}
	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, s.auth.client)
	tok, err := s.oauthConfig(r, c).Exchange(ctx, q.Get("code"), oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		log.Printf("admin: code exchange: %v", err)
		fail(http.StatusBadGateway, "Unavailable", "Sign-in could not be completed. Try again in a moment.")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	id, err := s.idVerifier().Verify(oidc.ClientContext(r.Context(), s.auth.client), raw)
	if err != nil {
		log.Printf("admin: id_token: %v", err)
		fail(http.StatusUnauthorized, "Refused", "Sign-in could not be verified.")
		return
	}
	if subtle.ConstantTimeCompare([]byte(id.Nonce), []byte(flow.Nonce)) != 1 || id.Subject == "" {
		fail(http.StatusUnauthorized, "Refused", "Sign-in could not be verified.")
		return
	}
	issuer := s.cfg.admin.issuer
	principal := fmt.Sprintf("%d:%s%s", len(issuer), issuer, id.Subject)
	ok, err := s.permitted(r.Context(), principal, true)
	if err != nil {
		log.Printf("admin: keto: %v", err)
		fail(http.StatusServiceUnavailable, "Unavailable", "The permission service could not be reached. Try again in a moment.")
		return
	}
	if !ok {
		s.errorPage(w, http.StatusForbidden, "Refused", "Your account can't open the dashboard.", false)
		return
	}
	session := sessionClaims{Principal: principal, Expires: s.auth.now().Add(sessionLife).Unix()}
	s.setCookie(w, r, sessionCookie, seal(c.key, "session", session), "/", sessionLife)
	http.Redirect(w, r, safeReturn(flow.Return), http.StatusSeeOther)
}

func (s *server) signOut(w http.ResponseWriter, r *http.Request, _ *adminCreds, _ string) {
	if r.Header.Get("Origin") != s.ownOrigin(r) {
		s.errorPage(w, http.StatusForbidden, "Refused", "Sign-out must come from the site's own page.", false)
		return
	}
	s.setCookie(w, r, sessionCookie, "", "/", 0)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) mountAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", s.admin(true, s.requestsPage))
	mux.HandleFunc("GET /admin/requests.csv", s.admin(true, s.requestsCSV))
	mux.HandleFunc("GET /admin/pages", s.admin(true, s.pagesPage))
	mux.HandleFunc("GET /admin/pages/{name}", s.admin(true, s.preview))
	mux.HandleFunc("GET /admin/callback", s.admin(false, s.callback))
	mux.HandleFunc("POST /admin/sign-out", s.admin(false, s.signOut))
	mux.HandleFunc("GET /admin/admin.css", s.admin(false, s.adminStylesheet))
}

// ------------------------------------------------------------------ pages --

type navItem struct {
	Label, Href string
	Active      bool
}

// The dashboard's sections. Traffic and Tests are added here.
var adminNav = []navItem{
	{Label: "Requests", Href: "/admin"},
	{Label: "Pages", Href: "/admin/pages"},
}

// What every dashboard page is rendered from; each page embeds it.
type adminFrame struct {
	Title     string
	Nav       []navItem
	SignedIn  bool
	StylesKey string
	AdminKey  string
}

var adminCSSKey string
var adminCSS []byte

func init() {
	b, err := site.ReadFile("site/admin.css")
	if err != nil {
		panic(err)
	}
	adminCSS = b
	sum := sha256.Sum256(b)
	adminCSSKey = hex.EncodeToString(sum[:8])
}

func (s *server) frame(title, active string, signedIn bool) adminFrame {
	f := adminFrame{Title: title, SignedIn: signedIn, StylesKey: stylesKey, AdminKey: adminCSSKey}
	if signedIn {
		for _, n := range adminNav {
			n.Active = n.Label == active
			f.Nav = append(f.Nav, n)
		}
	}
	return f
}

var adminFiles = func() fs.FS {
	f, err := fs.Sub(site, "site/admin")
	if err != nil {
		panic(err)
	}
	return f
}()

func adminTemplate(name string) *template.Template {
	return template.Must(template.New("").ParseFS(adminFiles, "layout.html", name+".html"))
}

var (
	requestsTmpl = adminTemplate("requests")
	pagesTmpl    = adminTemplate("pages")
	errorTmpl    = adminTemplate("error")
)

func (s *server) renderAdmin(w http.ResponseWriter, status int, t *template.Template, data any) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		log.Printf("admin: render: %v", err)
		http.Error(w, "the page could not be drawn", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

func (s *server) errorPage(w http.ResponseWriter, status int, title, message string, retry bool) {
	adminHeaders(w)
	s.renderAdmin(w, status, errorTmpl, struct {
		adminFrame
		Status  int
		Message string
		Retry   bool
	}{s.frame(title, "", false), status, message, retry})
}

func (s *server) adminStylesheet(w http.ResponseWriter, r *http.Request, _ *adminCreds, _ string) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	if r.URL.Query().Get("v") == adminCSSKey {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.Write(adminCSS)
}

// -------------------------------------------------------------- requests --

// One line of the file as the table and the CSV show it, so they cannot
// differ.
type requestRow struct {
	Line       int
	Unreadable bool
	Stamp      string // RFC 3339, UTC
	Day        string
	accessRequest
}

func (s *server) readRequests() ([]requestRow, error) {
	if s.store == nil {
		return nil, nil
	}
	f, err := s.store.open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows []requestRow
	br := bufio.NewReader(f)
	for n := 1; ; n++ {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			row := requestRow{Line: n}
			if json.Unmarshal(line, &row.accessRequest) != nil || row.Email == "" {
				row = requestRow{Line: n, Unreadable: true}
			} else {
				at := row.accessRequest.At.UTC()
				row.Stamp, row.Day = at.Format(time.RFC3339), at.Format("2006-01-02 15:04")
			}
			rows = append(rows, row)
		}
		if err != nil {
			if err != io.EOF {
				return nil, err
			}
			break
		}
	}
	// The file is in the order the requests came: newest first is its reverse.
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows, nil
}

var interestOrder = []string{"use", "partner", "invest", "other"}

// The rows for a filter, which is an interest or empty for all.
func filterRows(rows []requestRow, interest string) []requestRow {
	if interest == "" {
		return rows
	}
	var out []requestRow
	for _, r := range rows {
		if !r.Unreadable && r.Interest == interest {
			out = append(out, r)
		}
	}
	return out
}

func filterFrom(r *http.Request) string {
	if v := r.URL.Query().Get("interest"); interests[v] {
		return v
	}
	return ""
}

func (s *server) requestsPage(w http.ResponseWriter, r *http.Request, _ *adminCreds, _ string) {
	all, err := s.readRequests()
	if err != nil {
		log.Printf("admin: requests: %v", err)
		s.errorPage(w, http.StatusInternalServerError, "Error", "The requests could not be read.", true)
		return
	}
	interest := filterFrom(r)
	rows := filterRows(all, interest)
	csvLink := "/admin/requests.csv"
	if interest != "" {
		csvLink += "?interest=" + interest
	}
	s.renderAdmin(w, http.StatusOK, requestsTmpl, struct {
		adminFrame
		Rows      []requestRow
		Total     int
		Interest  string
		Interests []string
		CSV       string
		Stored    bool
	}{s.frame("Requests", "Requests", true), rows, len(all), interest, interestOrder, csvLink, s.store != nil})
}

// A cell that a spreadsheet would read as a formula is made plain text.
func cell(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

func (s *server) requestsCSV(w http.ResponseWriter, r *http.Request, _ *adminCreds, _ string) {
	all, err := s.readRequests()
	if err != nil {
		http.Error(w, "the requests could not be read", http.StatusInternalServerError)
		return
	}
	rows := filterRows(all, filterFrom(r))
	var buf bytes.Buffer
	out := csv.NewWriter(&buf)
	out.Write([]string{"date", "email", "name", "github", "interest", "note", "page", "test", "arm"})
	for _, row := range rows {
		if row.Unreadable {
			out.Write([]string{"", "", "", "", "", fmt.Sprintf("unreadable line %d", row.Line), "", "", ""})
			continue
		}
		out.Write([]string{row.Stamp, cell(row.Email), cell(row.Name), cell(row.GitHub), row.Interest,
			cell(row.About), row.Page, row.Test, row.Arm})
	}
	out.Flush()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="requests.csv"`)
	w.Write(buf.Bytes())
}

// ----------------------------------------------------------------- pages --

type pageRow struct {
	Name  string
	Path  string
	Tests []string
}

func (s *server) pagesPage(w http.ResponseWriter, r *http.Request, _ *adminCreds, _ string) {
	var ids []string
	for id := range s.reg.tests {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var rows []pageRow
	for name, p := range s.reg.pages {
		row := pageRow{Name: name, Path: p.def.Path}
		for _, id := range ids {
			if s.reg.tests[id].hasArm(name) {
				row.Tests = append(row.Tests, id)
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	s.renderAdmin(w, http.StatusOK, pagesTmpl, struct {
		adminFrame
		Rows []pageRow
	}{s.frame("Pages", "Pages", true), rows})
}

// Renders a page as a visitor sees it, marked as a preview, with no test and
// no assignment cookie.
func (s *server) preview(w http.ResponseWriter, r *http.Request, _ *adminCreds, _ string) {
	p := s.reg.pages[r.PathValue("name")]
	if p == nil {
		s.errorPage(w, http.StatusNotFound, "Not found", "No page has that name.", false)
		return
	}
	route := p.def.Path
	if route == "" {
		route = "/"
	}
	s.render(w, p, route, "", "", true)
}
