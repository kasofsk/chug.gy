# chug.gy

chuggy's public site, and the form that asks for an invite. One static Go
binary serves the pages embedded in it, keeps each request for access as one
JSON line in a file, and serves that file back to the holder of an admin
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
| `CHUG_SITE_DATA` | The directory `requests.jsonl` is kept in. Unset, the form answers 503. |
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
  policy headers and the redirects for short paths such as `/linux`.
- `pages.go` declares the pages and A/B tests, checks them when the server
  starts, and serves them.
- `admin.go` is the dashboard: sign-in with Hydra, the Keto check, the signed
  cookies, and the Requests and Pages pages.
- `docs.go` renders the docs: the README's list is the sidebar, links between
  pages are rewritten to their paths here, and `llms.txt` names the pages
  here. `just run` reads a checkout beside this repository.
- `site/` is embedded into the binary: `pages/` (one template per page, such
  as `home.html`), `partials/` (the request form and the footer), `requested.html`,
  `docs.html`, `admin/` (the dashboard's templates) with `admin.css`, `site.css`, `site.js`, the fonts and the favicon. The work
  graph in the hero is inline SVG in `pages/home.html`.

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
way, with no test and no cookie.

**Declare a test.** Add a `testDef` to `testDefs` in `pages.go`: an ID, the
route, and arms (a registered page and a positive weight each; the first is the
baseline). A test's route takes over a page's own path, so `/` serves the
visitor's arm in place. A bad declaration fails `go test` and stops the binary
starting.

**Changing a test's arms means giving it a new ID**, so results from different
arms never mix.

A test sets the cookie `chug_test_<id>` (90 days, `HttpOnly`), which holds only
the arm's page name. The form records `page`, `test` and `arm` in each request's
line when they check out.

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
pages and links each to a preview. A person signs in with their chuggy account
(authorization code with PKCE, client `chuggy-chug-gy`); the site then asks
Keto whether `Site:main#read_marketing` holds for them, and asks again on every
request, remembering the answer for up to a minute. If Keto cannot be reached
the answer is 503. The session is a cookie signed with the session key, good
for 12 hours. Locally, on loopback, the redirect URI is
`http://<host>/admin/callback`, which only a Hydra client registered with it,
or a fake, will complete. Traffic and Tests slot in through `adminNav` in
`admin.go`.
