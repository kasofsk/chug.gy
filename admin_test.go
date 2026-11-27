package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	testIssuer = "https://auth.vteng.io"
	testClient = "chuggy-chug-gy"
	testSecret = "s3cret-value"
	testSub    = "person-1"
	testPrince = "21:https://auth.vteng.io" + testSub
)

// A Hydra's token endpoint and keys, signing real JWTs with a test key.
type fakeHydra struct {
	*httptest.Server
	key *rsa.PrivateKey

	mu     sync.Mutex
	codes  map[string]fakeCode
	claims func(c map[string]any) // changes the id_token's claims
}

type fakeCode struct{ nonce, challenge, redirect string }

func newFakeHydra(t *testing.T) *fakeHydra {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeHydra{key: key, codes: map[string]fakeCode{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}}
		json.NewEncoder(w).Encode(set)
	})
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if !ok || id != testClient || secret != testSecret {
			http.Error(w, `{"error":"invalid_client"}`, 401)
			return
		}
		r.ParseForm()
		f.mu.Lock()
		code, found := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		mod := f.claims
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !found || r.PostForm.Get("grant_type") != "authorization_code" ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != code.challenge ||
			r.PostForm.Get("redirect_uri") != code.redirect {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		claims := map[string]any{
			"iss": testIssuer, "aud": []string{testClient}, "sub": testSub, "nonce": code.nonce,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		}
		if mod != nil {
			mod(claims)
		}
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "k1"}}, (&jose.SignerOptions{}).WithType("JWT"))
		if err != nil {
			t.Error(err)
			return
		}
		raw, err := jwt.Signed(signer).Claims(claims).Serialize()
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "a", "token_type": "bearer", "expires_in": 3600, "id_token": raw})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// Keto's check: what it says, and who was asked.
type fakeKeto struct {
	*httptest.Server
	mu      sync.Mutex
	allowed bool
	status  int
	body    string
	asked   []url.Values
}

func newFakeKeto(t *testing.T) *fakeKeto {
	k := &fakeKeto{allowed: true}
	k.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k.mu.Lock()
		defer k.mu.Unlock()
		if r.URL.Path != "/relation-tuples/check/openapi" {
			http.NotFound(w, r)
			return
		}
		k.asked = append(k.asked, r.URL.Query())
		if k.status != 0 {
			w.WriteHeader(k.status)
		}
		if k.body != "" {
			w.Write([]byte(k.body))
			return
		}
		json.NewEncoder(w).Encode(map[string]bool{"allowed": k.allowed})
	}))
	t.Cleanup(k.Close)
	return k
}

func (k *fakeKeto) set(allowed bool) { k.mu.Lock(); k.allowed = allowed; k.mu.Unlock() }
func (k *fakeKeto) count() int       { k.mu.Lock(); defer k.mu.Unlock(); return len(k.asked) }

type adminEnv struct {
	s     *server
	h     http.Handler
	hydra *fakeHydra
	keto  *fakeKeto
	dir   string
	now   time.Time
	key   []byte
	cfg   config
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	dir := t.TempDir()
	e := &adminEnv{hydra: newFakeHydra(t), keto: newFakeKeto(t), dir: dir, now: time.Now(),
		key: []byte("0123456789abcdef0123456789abcdef-key")}
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	e.cfg = config{
		dataDir: filepath.Join(dir, "data"), host: "chug.gy",
		adminTokenFile: write("token", []byte("0123456789abcdef0123456789abcdef\n")),
		admin: adminConfig{
			issuer: testIssuer, internalURL: e.hydra.URL, clientID: testClient,
			secretFile: write("secret", []byte(testSecret+"\n")), sessionKeyFile: write("key", e.key),
			ketoURL: e.keto.URL,
		},
	}
	e.start(t)
	return e
}

func (e *adminEnv) start(t *testing.T) {
	t.Helper()
	keepTmpdir(t)
	s, err := newServer(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.auth.now = func() time.Time { return e.now }
	e.s, e.h = s, s.handler()
}

// A browser: it keeps the cookies the site sets and sends them back.
type browser struct {
	e       *adminEnv
	host    string
	cookies map[string]*http.Cookie
}

func (e *adminEnv) browser() *browser {
	return &browser{e: e, host: "chug.gy", cookies: map[string]*http.Cookie{}}
}

func (b *browser) do(method, target string, header map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Host = b.host
	for _, c := range b.cookies {
		r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	b.e.h.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c
		}
	}
	return w
}

// Starts a sign-in at path and returns Hydra's authorization request.
func (b *browser) begin(t *testing.T, path string) url.Values {
	t.Helper()
	w := b.do("GET", path, nil)
	if w.Code != 302 {
		t.Fatalf("%s answered %d, wanted a redirect to sign in", path, w.Code)
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil || loc.Scheme+"://"+loc.Host+loc.Path != testIssuer+"/oauth2/auth" {
		t.Fatalf("sign-in went to %q", w.Header().Get("Location"))
	}
	return loc.Query()
}

// What Hydra does once the person agrees: a code for this request.
func (e *adminEnv) approve(q url.Values) string {
	code := "code-" + q.Get("state")
	e.hydra.mu.Lock()
	e.hydra.codes[code] = fakeCode{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri")}
	e.hydra.mu.Unlock()
	return code
}

func (b *browser) callback(q url.Values, code, state string) *httptest.ResponseRecorder {
	return b.do("GET", "/admin/callback?"+url.Values{"code": {code}, "state": {state}}.Encode(), nil)
}

func (b *browser) signIn(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	q := b.begin(t, path)
	return b.callback(q, b.e.approve(q), q.Get("state"))
}

func (b *browser) mustSignIn(t *testing.T) {
	t.Helper()
	if w := b.signIn(t, "/admin"); w.Code != 303 || w.Header().Get("Location") != "/admin" {
		t.Fatalf("sign-in ended %d %s: %s", w.Code, w.Header().Get("Location"), w.Body)
	}
}

func seedRequests(t *testing.T, e *adminEnv, lines ...string) {
	t.Helper()
	os.MkdirAll(e.cfg.dataDir, 0o700)
	if err := os.WriteFile(filepath.Join(e.cfg.dataDir, "requests.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRoundTrip(t *testing.T) {
	e := newAdminEnv(t)
	b := e.browser()
	q := b.begin(t, "/admin/pages?x=1")
	for k, want := range map[string]string{
		"response_type": "code", "scope": "openid", "client_id": testClient,
		"code_challenge_method": "S256", "redirect_uri": "https://chug.gy/admin/callback",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	if q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge") == "" {
		t.Errorf("the request lacks state, nonce or challenge: %v", q)
	}
	r := httptest.NewRequest("GET", "/admin", nil)
	r.Host = "chug.gy"
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	fc := w.Result().Cookies()[0]
	if !fc.HttpOnly || !fc.Secure || fc.SameSite != http.SameSiteLaxMode || fc.Path != "/admin" || fc.MaxAge != 600 {
		t.Errorf("the flow cookie is %+v", fc)
	}

	w = b.callback(q, e.approve(q), q.Get("state"))
	if w.Code != 303 || w.Header().Get("Location") != "/admin/pages?x=1" {
		t.Fatalf("callback answered %d %s: %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	sc := b.cookies[sessionCookie]
	if sc == nil || !sc.HttpOnly || !sc.Secure || sc.SameSite != http.SameSiteLaxMode || sc.Path != "/" || sc.MaxAge != 12*3600 {
		t.Fatalf("the session cookie is %+v", sc)
	}
	if b.cookies[flowCookie] != nil {
		t.Error("the flow cookie outlived the flow")
	}
	var claims sessionClaims
	if !unseal(e.key, "session", sc.Value, &claims) || claims.Principal != testPrince {
		t.Errorf("the session names %+v, want %s", claims, testPrince)
	}
	e.keto.mu.Lock()
	asked := e.keto.asked[0]
	e.keto.mu.Unlock()
	if asked.Get("namespace") != "Site" || asked.Get("object") != "main" || asked.Get("relation") != "read_marketing" || asked.Get("subject_id") != testPrince {
		t.Errorf("keto was asked %v", asked)
	}
	w = b.do("GET", "/admin/pages", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "chug.gy admin") {
		t.Fatalf("the page answered %d", w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Robots-Tag") != "noindex" {
		t.Errorf("admin headers: %v", w.Header())
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "form-action 'self'") {
		t.Error("the policy changed")
	}
}

func TestLoopbackRedirectAndCookies(t *testing.T) {
	e := newAdminEnv(t)
	b := e.browser()
	b.host = "127.0.0.1:8080"
	q := b.begin(t, "/admin")
	if q.Get("redirect_uri") != "http://127.0.0.1:8080/admin/callback" {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if c := b.cookies[flowCookie]; c.Secure {
		t.Error("the flow cookie is Secure on loopback")
	}
	if w := b.callback(q, e.approve(q), q.Get("state")); w.Code != 303 {
		t.Fatalf("callback answered %d: %s", w.Code, w.Body)
	}
	if b.cookies[sessionCookie].Secure {
		t.Error("the session cookie is Secure on loopback")
	}
}

func TestRefusedByKeto(t *testing.T) {
	e := newAdminEnv(t)
	e.keto.set(false)
	b := e.browser()
	w := b.signIn(t, "/admin")
	if w.Code != 403 || !strings.Contains(w.Body.String(), "can&#39;t open the dashboard") {
		t.Fatalf("answered %d: %s", w.Code, w.Body)
	}
	if b.cookies[sessionCookie] != nil {
		t.Error("a refused person got a session")
	}
	if w := b.do("GET", "/admin", nil); w.Code != 302 {
		t.Errorf("the page without a session answered %d", w.Code)
	}
}

func TestKetoDown(t *testing.T) {
	for name, set := range map[string]func(k *fakeKeto){
		"error status": func(k *fakeKeto) { k.status = 500 },
		"not json":     func(k *fakeKeto) { k.body = "nope" },
		"no answer":    func(k *fakeKeto) { k.body = "{}" },
	} {
		t.Run(name, func(t *testing.T) {
			e := newAdminEnv(t)
			b := e.browser()
			b.mustSignIn(t)
			set(e.keto)
			e.now = e.now.Add(2 * ketoTTL)
			if w := b.do("GET", "/admin", nil); w.Code != 503 {
				t.Errorf("the page answered %d", w.Code)
			}
			if w := e.browser().signIn(t, "/admin"); w.Code != 503 {
				t.Errorf("sign-in answered %d", w.Code)
			}
		})
	}
	e := newAdminEnv(t)
	b := e.browser()
	b.mustSignIn(t)
	e.keto.Close()
	e.now = e.now.Add(2 * ketoTTL)
	if w := b.do("GET", "/admin", nil); w.Code != 503 {
		t.Errorf("with keto gone the page answered %d", w.Code)
	}
}

func TestBadStateOrCookie(t *testing.T) {
	e := newAdminEnv(t)
	b := e.browser()
	for name, state := range map[string]string{"wrong": "other", "missing": ""} {
		b := e.browser()
		q := b.begin(t, "/admin")
		if w := b.callback(q, e.approve(q), state); w.Code != 400 {
			t.Errorf("%s state answered %d", name, w.Code)
		}
	}
	q := b.begin(t, "/admin")
	code := e.approve(q)
	// No flow cookie at all.
	fresh := e.browser()
	if w := fresh.callback(q, code, q.Get("state")); w.Code != 400 {
		t.Errorf("no cookie answered %d", w.Code)
	}
	// A forged flow cookie.
	fresh.cookies[flowCookie] = &http.Cookie{Name: flowCookie, Value: seal([]byte("another key another key another"), "flow",
		flowClaims{State: "s", Return: "/admin", Expires: e.now.Add(time.Hour).Unix()})}
	if w := fresh.callback(q, code, "s"); w.Code != 400 {
		t.Errorf("a forged cookie answered %d", w.Code)
	}
	// An expired flow.
	b = e.browser()
	q = b.begin(t, "/admin")
	code = e.approve(q)
	e.now = e.now.Add(11 * time.Minute)
	if w := b.callback(q, code, q.Get("state")); w.Code != 400 {
		t.Errorf("an old flow answered %d", w.Code)
	}
	// A refusal at Hydra.
	e.now = time.Now()
	b = e.browser()
	q = b.begin(t, "/admin")
	if w := b.do("GET", "/admin/callback?error=access_denied&state="+q.Get("state"), nil); w.Code != 400 {
		t.Errorf("an error from hydra answered %d", w.Code)
	}
}

func TestBadIDTokens(t *testing.T) {
	for name, mod := range map[string]func(map[string]any){
		"nonce":    func(c map[string]any) { c["nonce"] = "someone else's" },
		"audience": func(c map[string]any) { c["aud"] = []string{"another-client"} },
		"issuer":   func(c map[string]any) { c["iss"] = "https://evil.example" },
		"expired":  func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() },
	} {
		t.Run(name, func(t *testing.T) {
			e := newAdminEnv(t)
			e.hydra.claims = mod
			b := e.browser()
			w := b.signIn(t, "/admin")
			if w.Code != 401 {
				t.Errorf("answered %d: %s", w.Code, w.Body)
			}
			if b.cookies[sessionCookie] != nil {
				t.Error("a session was set")
			}
			if e.keto.count() != 0 {
				t.Error("keto was asked about an unverified person")
			}
		})
	}
}

func TestCodeNotRedeemable(t *testing.T) {
	e := newAdminEnv(t)
	b := e.browser()
	q := b.begin(t, "/admin")
	if w := b.callback(q, "made-up", q.Get("state")); w.Code != 502 {
		t.Errorf("an unknown code answered %d", w.Code)
	}
}

func TestSessionTamperedOrExpired(t *testing.T) {
	e := newAdminEnv(t)
	b := e.browser()
	b.mustSignIn(t)
	good := b.cookies[sessionCookie].Value

	payload, _, _ := strings.Cut(good, ".")
	forged := seal([]byte("not the session key, not at all"), "session", sessionClaims{Principal: testPrince, Expires: e.now.Add(time.Hour).Unix()})
	other, _ := json.Marshal(sessionClaims{Principal: "21:https://auth.vteng.iostranger", Expires: e.now.Add(time.Hour).Unix()})
	for name, value := range map[string]string{
		"forged":    forged,
		"swapped":   base64.RawURLEncoding.EncodeToString(other) + "." + strings.SplitN(good, ".", 2)[1],
		"unsigned":  payload,
		"flow kind": seal(e.key, "flow", sessionClaims{Principal: testPrince, Expires: e.now.Add(time.Hour).Unix()}),
	} {
		b.cookies[sessionCookie] = &http.Cookie{Name: sessionCookie, Value: value}
		if w := b.do("GET", "/admin", nil); w.Code != 302 {
			t.Errorf("a %s cookie answered %d", name, w.Code)
		}
	}
	b.cookies[sessionCookie] = &http.Cookie{Name: sessionCookie, Value: good}
	if w := b.do("GET", "/admin", nil); w.Code != 200 {
		t.Fatalf("the good cookie answered %d", w.Code)
	}
	e.now = e.now.Add(12*time.Hour + time.Second)
	if w := b.do("GET", "/admin", nil); w.Code != 302 {
		t.Errorf("an expired session answered %d", w.Code)
	}
}

func TestRevokedGrantAfterCache(t *testing.T) {
	e := newAdminEnv(t)
	b := e.browser()
	b.mustSignIn(t)
	b.do("GET", "/admin", nil)
	asked := e.keto.count()
	b.do("GET", "/admin/pages", nil)
	if e.keto.count() != asked {
		t.Error("keto was asked again inside the cache")
	}
	e.keto.set(false)
	e.now = e.now.Add(ketoTTL - time.Second)
	if w := b.do("GET", "/admin", nil); w.Code != 200 {
		t.Errorf("inside the cache the page answered %d", w.Code)
	}
	e.now = e.now.Add(2 * time.Second)
	if w := b.do("GET", "/admin", nil); w.Code != 403 {
		t.Errorf("after the cache the page answered %d", w.Code)
	}
}

func TestReturnPageStaysInAdmin(t *testing.T) {
	for in, want := range map[string]string{
		"/admin":                   "/admin",
		"/admin/pages?x=1":         "/admin/pages?x=1",
		"https://evil.example/":    "/admin",
		"//evil.example/admin":     "/admin",
		"/\\evil.example":          "/admin",
		"/administrator":           "/admin",
		"/":                        "/admin",
		"/admin/callback":          "/admin",
		"/admin/requests":          "/admin",
		"/admin/../x":              "/admin",
		"/admin//evil.example":     "/admin",
		"javascript:alert(1)":      "/admin",
		"/admin/pages\r\nX: y":     "/admin",
		"":                         "/admin",
		"/admin?interest=use":      "/admin?interest=use",
		"/admin/pages/home":        "/admin/pages/home",
		"http://chug.gy/admin":     "/admin",
		"/admin/sign-out":          "/admin",
		"/admin/%2e%2e/%2e%2e/etc": "/admin",
	} {
		if got := safeReturn(in); got != want {
			t.Errorf("safeReturn(%q) = %q, want %q", in, got, want)
		}
	}
	// Even a signed cookie that names another site is not followed.
	e := newAdminEnv(t)
	b := e.browser()
	q := b.begin(t, "/admin")
	b.cookies[flowCookie] = &http.Cookie{Name: flowCookie, Value: seal(e.key, "flow", flowClaims{
		State: q.Get("state"), Nonce: q.Get("nonce"), Verifier: "unused-and-too-short-but-ignored",
		Return: "https://evil.example/", Expires: e.now.Add(time.Minute).Unix(),
	})}
	// The verifier differs from the one the challenge was made with, so redeeming fails.
	if w := b.callback(q, e.approve(q), q.Get("state")); w.Code == 303 && strings.Contains(w.Header().Get("Location"), "evil") {
		t.Errorf("left the site: %s", w.Header().Get("Location"))
	}
}

func TestAdminOffWithoutConfiguration(t *testing.T) {
	paths := []string{"/admin", "/admin/", "/admin/pages", "/admin/pages/home", "/admin/traffic", "/admin/tests", "/admin/requests.csv", "/admin/callback", "/admin/admin.css"}
	breakers := map[string]func(e *adminEnv){
		"issuer":      func(e *adminEnv) { e.cfg.admin.issuer = "" },
		"internal":    func(e *adminEnv) { e.cfg.admin.internalURL = "" },
		"client":      func(e *adminEnv) { e.cfg.admin.clientID = "" },
		"secret file": func(e *adminEnv) { e.cfg.admin.secretFile = "" },
		"secret gone": func(e *adminEnv) { e.cfg.admin.secretFile = filepath.Join(e.dir, "absent") },
		"key file":    func(e *adminEnv) { e.cfg.admin.sessionKeyFile = "" },
		"key short":   func(e *adminEnv) { os.WriteFile(e.cfg.admin.sessionKeyFile, []byte("too short\n"), 0o600) },
		"keto":        func(e *adminEnv) { e.cfg.admin.ketoURL = "" },
		"host":        func(e *adminEnv) { e.cfg.host = "" },
	}
	for name, breakIt := range breakers {
		t.Run(name, func(t *testing.T) {
			e := newAdminEnv(t)
			breakIt(e)
			e.start(t)
			seedRequests(t, e, `{"at":"2026-01-02T03:04:05Z","email":"a@b.co","interest":"use"}`)
			for _, p := range paths {
				if w := get(e.h, "chug.gy", p); w.Code != 404 {
					t.Errorf("GET %s answered %d", p, w.Code)
				}
			}
			r := httptest.NewRequest("POST", "/admin/sign-out", nil)
			r.Host = "chug.gy"
			r.Header.Set("Origin", "https://chug.gy")
			w := httptest.NewRecorder()
			e.h.ServeHTTP(w, r)
			if w.Code != 404 {
				t.Errorf("sign-out answered %d", w.Code)
			}
			assertBearerRoute(t, e)
		})
	}
}

// The bearer route answers as it always has, whatever the dashboard is doing.
func assertBearerRoute(t *testing.T, e *adminEnv) {
	t.Helper()
	if w := get(e.h, "chug.gy", "/admin/requests"); w.Code != 401 {
		t.Errorf("the bearer route without a token answered %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/admin/requests", nil)
	r.Host = "chug.gy"
	r.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/x-ndjson") ||
		!strings.Contains(w.Body.String(), "a@b.co") {
		t.Errorf("the bearer route with a token answered %d %q", w.Code, w.Body)
	}
}

func TestBearerRouteUnchanged(t *testing.T) {
	e := newAdminEnv(t)
	seedRequests(t, e, `{"at":"2026-01-02T03:04:05Z","email":"a@b.co","interest":"use"}`)
	assertBearerRoute(t, e)
	b := e.browser()
	b.mustSignIn(t)
	assertBearerRoute(t, e) // a session changes nothing
	e.keto.set(false)
	e.keto.status = 500
	assertBearerRoute(t, e)
	// A session cookie is not a token, and the route never sends anyone to sign in.
	if w := b.do("GET", "/admin/requests", nil); w.Code != 401 {
		t.Errorf("a session on the bearer route answered %d", w.Code)
	}
}

func TestRequestsPageAndCSV(t *testing.T) {
	e := newAdminEnv(t)
	seedRequests(t, e,
		`{"at":"2026-01-01T10:00:00Z","email":"old@x.co","name":"Old One","github":"octocat","interest":"use","about":"first"}`,
		`this is not json`,
		`{"at":"2026-01-02T10:00:00Z","email":"mid@x.co","interest":"use"}`,
		`{"at":"2026-01-03T10:00:00Z","email":"new@x.co","name":"=HYPERLINK(\"x\")","interest":"invest","about":"a very long note\nover lines <b>not bold</b> `+strings.Repeat("long ", 200)+`end","page":"home","test":"t1","arm":"alt"}`,
	)
	b := e.browser()
	b.mustSignIn(t)
	w := b.do("GET", "/admin", nil)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("answered %d", w.Code)
	}
	order := []string{"new@x.co", "mid@x.co", "Unreadable line 2", "old@x.co"}
	last := -1
	for _, want := range order {
		i := strings.Index(body, want)
		if i < 0 || i < last {
			t.Fatalf("%q is missing or out of order (newest first) at %d", want, i)
		}
		last = i
	}
	for _, want := range []string{
		`href="mailto:old@x.co"`, `href="https://github.com/octocat"`, ">Old One<", "&lt;b&gt;not bold&lt;/b&gt;",
		"end</td>", ">alt<", ">t1<", ">home<", `href="/admin/requests.csv"`, ">4<", "Sign out", ">Requests<", ">Pages<",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the table lacks %s", want)
		}
	}
	if strings.Contains(body, "<script") || strings.Contains(body, " style=") || strings.Contains(body, "<style") {
		t.Error("the page has inline script or style")
	}

	c := b.do("GET", "/admin/requests.csv", nil)
	if c.Code != 200 || !strings.HasPrefix(c.Header().Get("Content-Type"), "text/csv") || c.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("csv answered %d %v", c.Code, c.Header())
	}
	recs, err := csv.NewReader(c.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 5 {
		t.Fatalf("csv has %d records, want a header and four rows", len(recs))
	}
	rows, _ := e.s.readRequests()
	for i, row := range rows {
		rec := recs[i+1]
		if row.Unreadable {
			if rec[5] != "unreadable line 2" || rec[1] != "" {
				t.Errorf("unreadable row is %v", rec)
			}
			continue
		}
		if rec[0] != row.Stamp || rec[1] != row.Email || rec[4] != row.Interest || rec[7] != row.Test || rec[8] != row.Arm {
			t.Errorf("row %d: csv %v does not match the table's %+v", i, rec, row)
		}
		if !strings.Contains(body, ">"+row.Day+"<") {
			t.Errorf("the table lacks the date %s", row.Day)
		}
	}
	if recs[1][1] != "new@x.co" || recs[1][2] != `'=HYPERLINK("x")` || !strings.Contains(recs[1][5], "\nover lines") {
		t.Errorf("first csv row: %q", recs[1])
	}

	// The filter shows the same rows in both.
	f := b.do("GET", "/admin?interest=invest", nil).Body.String()
	if !strings.Contains(f, "new@x.co") || strings.Contains(f, "mid@x.co") || strings.Contains(f, "Unreadable") || !strings.Contains(f, "1 of 4") {
		t.Error("the filter did not narrow the table")
	}
	if !strings.Contains(f, `href="/admin/requests.csv?interest=invest"`) {
		t.Error("the download does not carry the filter")
	}
	recs, _ = csv.NewReader(b.do("GET", "/admin/requests.csv?interest=invest", nil).Body).ReadAll()
	if len(recs) != 2 || recs[1][1] != "new@x.co" {
		t.Errorf("the filtered csv is %v", recs)
	}
}

func TestPagesAndPreview(t *testing.T) {
	dir := t.TempDir()
	cfg := config{dataDir: dir, host: "chug.gy"}
	e := newAdminEnv(t)
	cfg.admin = e.cfg.admin
	keepTmpdir(t)
	// "lone" is served by no slot, only at its own path.
	files := testFiles(t)
	home, err := fs.ReadFile(files, "pages/home.html")
	if err != nil {
		t.Fatal(err)
	}
	files = overlay{files, map[string]string{"pages/lone.html": string(home)}}
	pages := append(append([]pageDef{}, twoPages...), pageDef{Name: "lone", Path: "/lone"})
	s, err := newServerWith(cfg, files, pages, append(abSlot("/"), slotDef{Route: "/x", Page: "alt"}), []testDef{abTest()})
	if err != nil {
		t.Fatal(err)
	}
	e.s, e.h = s, s.handler()
	b := e.browser()
	b.mustSignIn(t)

	list := b.do("GET", "/admin/pages", nil).Body.String()
	for _, want := range []string{`href="/admin/pages/home"`, `href="/admin/pages/alt"`,
		`<a href="/home"><code>/home</code></a>`, `<a href="/alt"><code>/alt</code></a>`,
		"<code>/</code> through test t1, <code>/x</code></td>", // alt: through the test, and directly
		`<a href="/lone"><code>/lone</code></a></td>
<td><span class="none">none</span></td>`, // lone: no slot
	} {
		if !strings.Contains(list, want) {
			t.Errorf("the list lacks %s", want)
		}
	}
	for _, name := range []string{"home", "alt", "lone"} {
		w := b.do("GET", "/admin/pages/"+name, nil)
		if w.Code != 200 {
			t.Fatalf("%s preview answered %d", name, w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, `data-page="`+name+`" data-preview`) || strings.Contains(body, "data-test") || strings.Contains(body, "data-arm") {
			t.Errorf("%s preview's html tag: %.200s", name, body)
		}
		if !strings.Contains(body, `<link rel="canonical" href="https://chug.gy/`+name+`">`) {
			t.Errorf("%s preview's canonical is not its own path", name)
		}
		if len(w.Result().Cookies()) != 0 {
			t.Errorf("the preview set cookies: %v", w.Result().Cookies())
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Robots-Tag") != "noindex" {
			t.Errorf("the preview's headers: %v", w.Header())
		}
	}
	if w := b.do("GET", "/admin/pages/nope", nil); w.Code != 404 {
		t.Errorf("an unknown page answered %d", w.Code)
	}
	// The public page is unchanged: no data-preview.
	if strings.Contains(get(e.h, "chug.gy", "/alt").Body.String(), "data-preview") {
		t.Error("a visitor's page carries data-preview")
	}
	// A preview needs the session.
	if w := e.browser().do("GET", "/admin/pages/home", nil); w.Code != 302 {
		t.Errorf("a preview without a session answered %d", w.Code)
	}
}

func TestSignOut(t *testing.T) {
	e := newAdminEnv(t)
	b := e.browser()
	b.mustSignIn(t)
	// A foreign Origin, or none, is refused and keeps the session.
	for name, h := range map[string]map[string]string{
		"foreign": {"Origin": "https://evil.example"},
		"none":    nil,
		"http":    {"Origin": "http://chug.gy"},
	} {
		if w := b.do("POST", "/admin/sign-out", h); w.Code != 403 {
			t.Errorf("%s origin answered %d", name, w.Code)
		}
		if b.cookies[sessionCookie] == nil {
			t.Fatalf("%s origin cleared the session", name)
		}
	}
	// With the session expired, sign-out still answers itself and does not go to Hydra.
	e.now = e.now.Add(13 * time.Hour)
	w := b.do("POST", "/admin/sign-out", map[string]string{"Origin": "https://chug.gy"})
	if w.Code != 303 || w.Header().Get("Location") != "/" {
		t.Errorf("answered %d %s", w.Code, w.Header().Get("Location"))
	}
	if b.cookies[sessionCookie] != nil {
		t.Error("the session cookie was not cleared")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Error("sign-out is cacheable")
	}
	// And with no session at all.
	if w := e.browser().do("POST", "/admin/sign-out", map[string]string{"Origin": "https://chug.gy"}); w.Code != 303 {
		t.Errorf("without a session answered %d", w.Code)
	}
	// On loopback the site's own origin is the request's.
	l := e.browser()
	l.host = "localhost:9"
	if w := l.do("POST", "/admin/sign-out", map[string]string{"Origin": "http://localhost:9"}); w.Code != 303 {
		t.Errorf("loopback answered %d", w.Code)
	}
}

func TestAdminFramesAreNotPublic(t *testing.T) {
	e := newAdminEnv(t)
	for _, p := range []string{"/admin", "/admin/pages", "/admin/requests.csv", "/admin/pages/home"} {
		w := get(e.h, "chug.gy", p)
		if w.Code != 302 || strings.Contains(w.Body.String(), "@") {
			t.Errorf("%s answered %d", p, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Robots-Tag") != "noindex" {
			t.Errorf("%s redirect lacks the admin headers", p)
		}
	}
}

func TestChartSVGKeepsTextSize(t *testing.T) {
	svg := string(chartSVG([]dayCount{{Day: "2026-01-01", Visitors: 3, Views: 27420}, {Day: "2026-01-02", Visitors: 1, Views: 5}}))
	for _, bad := range []string{"viewBox", "style=", "<script"} {
		if strings.Contains(svg, bad) {
			t.Errorf("chart contains %q: %s", bad, svg)
		}
	}
	for _, want := range []string{`aria-label=`, `class="hit"`, `<title>`, `class="grid"`, ">40,000<", "27,420 views"} {
		if !strings.Contains(svg, want) {
			t.Errorf("chart lacks %q: %s", want, svg)
		}
	}
}

func TestChartSVGAxisIsBesideThePlot(t *testing.T) {
	svg := string(chartSVG([]dayCount{{Day: "2026-01-01", Visitors: 3, Views: 27420}, {Day: "2026-01-02", Visitors: 1, Views: 5}}))
	axisStart := strings.Index(svg, `<svg class="chartaxis" aria-hidden="true">`)
	plotStart := strings.Index(svg, `<svg class="chart" role="img"`)
	if axisStart < 0 || plotStart < axisStart {
		t.Fatalf("want the axis SVG before the plot SVG: %s", svg)
	}
	axis, plot := svg[axisStart:plotStart], svg[plotStart:]
	for _, label := range []string{">0<", ">20,000<", ">40,000<"} {
		if !strings.Contains(axis, label) || strings.Contains(plot, label) {
			t.Errorf("tick %s must be in the axis SVG only: %s", label, svg)
		}
	}
	if !strings.Contains(axis, `text-anchor="end"`) {
		t.Errorf("axis labels are not right-aligned: %s", axis)
	}
	if strings.Count(plot, `class="tick"`) != 2 {
		t.Errorf("the plot should hold only the two date labels: %s", plot)
	}
	// Both SVGs take their height from the stylesheet; they must share it.
	css, err := os.ReadFile("site/admin.css")
	if err != nil {
		t.Fatal(err)
	}
	h := regexp.MustCompile(`(?m)^\.(chartaxis|chart) \{[^}]*height: (\d+px)`).FindAllStringSubmatch(string(css), -1)
	if len(h) != 2 || h[0][2] != h[1][2] {
		t.Errorf("axis and plot heights differ: %v", h)
	}
}

func TestGroup(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 27420: "27,420", 100001: "100,001", 1234567: "1,234,567", -1500: "-1,500"} {
		if got := group(n); got != want {
			t.Errorf("group(%d) = %q, want %q", n, got, want)
		}
	}
}
