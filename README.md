# chug.gy

chuggy's public site, and the form that asks for an invite. One static Go
binary serves the pages embedded in it, keeps each request for access as one
JSON line in a file, counts its own page views in a SQLite file, and serves that file back to the holder of an admin
token, or to a person signed in at `/admin`. It makes three kinds of call, all
plain HTTP inside the cluster: to chuggy's Hydra, to redeem a sign-in code and
read the keys that sign the id_token; and to chuggy's Keto, to ask whether the
person holds the site permit `read_marketing`.

The site is deployed on chuggy's fabric: `cluster/chug-gy/` in
[gdoteof/chuggy-fabric](https://github.com/gdoteof/chuggy-fabric) runs the
image `cluster/build-system/chug-gy-image-run.yaml` builds from a commit of
this repository, and the host `chug.gy` reaches it through the fabric's tunnel.

## Run it

```sh
just run
```

Serves on `:8080` with the form writing under `dist/` and the docs read from
`../chuggy-docs`, if that checkout exists. The environment configures it:

| Variable | Meaning |
| --- | --- |
| `CHUG_SITE_LISTEN` | The address to listen on. Default `:8080`. |
| `CHUG_SITE_DATA` | The directory `requests.jsonl`, `events.db`, the daily salt and `tmp/` are kept in. Unset, the form answers 503 and `POST /e` answers 204 and keeps nothing. |
| `CHUG_SITE_ADMIN_TOKEN_FILE` | A file holding the bearer token `GET /admin/requests` takes. Unset, the route is 404. |
| `CHUG_SITE_HOST` | The canonical host. A request for another host is redirected to it, except on loopback and for `/healthz`. |
| `CHUG_SITE_OIDC_ISSUER` | The issuer, `https://auth.vteng.io` on chug.gy. The browser is sent to `<issuer>/oauth2/auth`. |
| `CHUG_SITE_OIDC_INTERNAL_URL` | The same Hydra over the pod network, `http://hydra-public.ory.svc.cluster.local:4444`. The code is redeemed at `<it>/oauth2/token` and keys are read from `<it>/.well-known/jwks.json`; no discovery document is fetched. |
| `CHUG_SITE_OIDC_CLIENT_ID` | The Hydra client, `chuggy-chug-gy`. |
| `CHUG_SITE_OIDC_CLIENT_SECRET_FILE` | A file holding that client's secret. Read when needed, not at start. |
| `CHUG_SITE_SESSION_KEY_FILE` | A file holding a key of at least 32 bytes that signs the cookies. Read when needed. |
| `CHUG_SITE_KETO_READ_URL` | Keto's read API, `http://keto-read.ory.svc.cluster.local:4466`. |
| `CHUG_SITE_DOCS` | A checkout of [kasofsk/chuggy-docs](https://github.com/kasofsk/chuggy-docs), rendered at `/docs` with `llms.txt` beside it. Unset, both go to GitHub. On chug.gy the site's Deployment sets it from the docs image. |

The dashboard at `/admin` needs every `CHUG_SITE_OIDC_*`, `CHUG_SITE_SESSION_KEY_FILE`
and `CHUG_SITE_KETO_READ_URL` setting, `CHUG_SITE_HOST` too (it names the
callback), and both files present and long enough. While any is missing, every
admin page is a 404; `GET /admin/requests` with the bearer token is not affected.

```sh
go test ./...
docker build -t chug.gy .
```

## What is where

- `main.go` is the server: the routes, the form's checks, the file, the
  policy headers, the redirects for short paths such as `/linux`, the
  per-address limiters, and the cap on requests in flight (256; a page waits
  up to 2 seconds for a place, then gets 503 with `Retry-After`, and `POST /e`
  gets it at once; `/healthz` is never held).
- `pages.go` declares the pages and A/B tests, checks them when the server
  starts, and serves them.
- `admin.go` is the dashboard: sign-in with Hydra, the Keto check, the signed
  cookies, and the Requests and Pages pages.
- `admin_stats.go` is the dashboard's Traffic and Tests pages: the range and
  "Show all" parameters, the chart, and what each table shows. `stats.go` is
  what they read from `events.db` and count, with the Wilson interval and the
  chance an arm beats the baseline.
- `events.go` is the analytics: `POST /e`, the checks on an event, the daily
  visitor ID and the SQLite store, which the writer fills in batches of up to
  256 events per transaction.
- `docs.go` renders the docs: the README's list is the sidebar, links between
  pages are rewritten to their paths here, and `llms.txt` names the pages
  here. `just run` reads a checkout beside this repository.
- `site/` is embedded into the binary: `pages/` (one template per page, such
  as `home.html`), `partials/` (the request form and the footer), `requested.html`,
  `docs.html`, `admin/` (the dashboard's templates) with `admin.css`, `site.css`, `site.js`, `analytics.js` (the page events), the fonts, `img/` (the pages' images, served at `/img/`) and the favicon. The work
  graph in the hero is inline SVG in `pages/home.html`.
  `pages/orchestration.html` loads `orchestration.css` and no other stylesheet;
  its screenshots are `img/orchestration-*.webp`, and its font is Archivo
  (`fonts/archivo-latin.woff2`, under the SIL Open Font License).

- `.github/workflows/chuggy-proof.yml` is a workflow started only by hand
  (`workflow_dispatch`); it prints its commit and ref, then succeeds or fails
  as asked.
- `tools/workflowrun/` is the program that starts that workflow, waits for the
  run and exits with its result. The `workflow-run-work` and `workflow-run`
  workloads in `.chug/configurations/default.json` run it, and the plan
  `workflow-proof` uses the second as an evaluator.

## Pages and tests

**Add a page.** Write `site/pages/<name>.html`, a whole document (it may use
the partials in `site/partials/`: `{{template "form" .}}`, `{{template "footer" .}}`),
and register it in `pageDefs` in `pages.go` with the same name (lowercase
letters, digits, hyphens), optionally a `Path` of its own (exact; `/` is `GET /{$}`)
and a `Stylesheet` under `site/`, served with a `?v=` content-hash key. Every
page gets `.Name`, `.Canonical`, `.Test`, `.Arm`, `.StylesKey`, `.PageStylesheet`
and `.PageStylesKey`. Put `data-page="{{.Name}}"` and the `data-test`/`data-arm`
attributes on `<html>` as `home.html` does, and `data-preview` when `.Preview`
is set: the dashboard's preview at `/admin/pages/<name>` renders a page that
way, with no test and no cookie. The policy header allows a page only what
this site serves: no style or script written into the page, and no font, image
or script from another site. A page's images go in `site/img/`; a browser may
keep one for a day, so a changed image takes a new name.

**Declare a test.** Add a `testDef` to `testDefs` in `pages.go`: an ID, the
route, and arms (a registered page and a positive weight each; the first is the
baseline). A test's route takes over a page's own path, so `/` serves the
visitor's arm in place. A bad declaration fails `go test` and stops the binary
starting.

**Changing a test's arms means giving it a new ID**, so results from different
arms never mix.

The test `home-redesign` runs on `/`: half the visitors get `home`, the
baseline, and half get `orchestration`.

A test sets the cookie `chug_test_<id>` (90 days, `HttpOnly`), which holds only
the arm's page name. The form records `page`, `test` and `arm` in each request's
line when they check out.

## What is collected, and what is not

The site measures itself, with no outside script. Each page's own script
(`site/analytics.js`) tells this site's server when a page is opened, how far
it is scrolled (25, 50, 75 and 100 percent), how many seconds it was in view,
which marked links and buttons are clicked, and when someone starts the request
form; the server notes when a request is sent, and which test arm the visitor
saw. It also keeps the time, the country Cloudflare reports, the kind of device
(phone, tablet or computer), where the visitor came from (the referring site's
name, and any `utm_` tags in the link), and a visitor ID. The events are in
`events.db` in the data directory.

The visitor ID is a hash of a salt that is new every day, this site's name, the
address and the browser's User-Agent, cut to 16 bytes, as
[Plausible](https://plausible.io/data-policy) publishes. The salt is deleted
the next day, so a person can be counted within one day and cannot be followed
from one day to the next. No address and no User-Agent is stored. Known bots
are not counted. The email and the other fields of the form never enter the
event store; they go only to `requests.jsonl`, as before. A page opened as an
admin preview, or by a browser under automation, sends nothing.

The site sets two kinds of cookie: the test assignment `chug_test_<id>` (above)
and the admin sign-in cookies. The analytics sets none and keeps nothing in the
browser. In production there is no `/tmp`, so the server makes `tmp/` in the
data directory and sets `TMPDIR` to it before SQLite opens.

## Starting the proof workflow

A chuggy job given the credential `chuggy.github-workflow-runs` can start this
repository's workflows, which proves the credential works. `workflowrun` exits
0 if the run succeeded, 1 if it did not (or ran another commit than
`CHUG_COMMIT`), and 2 if it could not finish. By hand, with a token that may
run Actions workflows:

```sh
echo "$TOKEN" > /tmp/gh-token
GH_TOKEN_FILE=/tmp/gh-token go run ./tools/workflowrun \
  --repo kasofsk/chug.gy --outcome success
```

It uses the default branch, or the branch in `CHUG_REF` if set. The token file
is read again before every call.

## Reading the requests

Each line of `requests.jsonl` is one request: `at`, `email`, `name`, `github`,
`interest` (`use`, `partner`, `invest` or `other`) and `about`, and, when the
form can say, `page`, `test` and `arm`. With a token:

```sh
curl -H "Authorization: Bearer $TOKEN" https://chug.gy/admin/requests
```

## The dashboard

`/admin` is Requests (newest first, filtered by `?interest=`, and
`/admin/requests.csv` for the same rows); `/admin/pages` lists the registered
pages and links each to a preview; `/admin/traffic` and `/admin/tests` read
the events. A person signs in with their chuggy account
(authorization code with PKCE, client `chuggy-chug-gy`); the site then asks
Keto whether `Site:main#read_marketing` holds for them, and asks again on every
request, remembering the answer for up to a minute. If Keto cannot be reached
the answer is 503. The session is a cookie signed with the session key, good
for 12 hours. Locally, on loopback, the redirect URI is
`http://<host>/admin/callback`, which only a Hydra client registered with it,
or a fake, will complete.
`admin.go`.

**Traffic** (`?days=7`, `30` or `90`, default 30, today included) charts
visitors and views for each UTC day, with the range's totals, and lists pages,
referring sites, campaigns, countries and devices by visitors; `?all=pages`
(or another table's name) shows every row of that table instead of the top 10.
**Tests** has a section for each declared test, then for each test found in
the events that is no longer declared ("Ended"). Both count visitor-days: one
visitor ID on one UTC day. A test's counts are of the visitor-days with a
`view` carrying its test and arm, so no rate passes 1; each rate has its 95%
Wilson interval, and each arm after the baseline the chance it beats the
baseline (10,000 draws from Beta(1 + successes, 1 + failures), seeded by the
data, so a reload shows the same figure). An arm with fewer than 100 visitors
is greyed. Neither page names a winner.
