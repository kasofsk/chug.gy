# chug.gy

chuggy's public site, and the form that asks for an invite. One static Go
binary serves the pages embedded in it, keeps each request for access as one
JSON line in a file, and serves that file back to the holder of an admin
token. It opens no connection of its own.

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
| `CHUG_SITE_DOCS` | A checkout of [kasofsk/chuggy-docs](https://github.com/kasofsk/chuggy-docs), rendered at `/docs` with `llms.txt` beside it. Unset, both go to GitHub. On chug.gy the site's Deployment sets it from the docs image. |

```sh
go test ./...
docker build -t chug.gy .
```

## What is where

- `main.go` is the server: the routes, the form's checks, the file, the
  policy headers and the redirects for short paths such as `/linux`.
- `pages.go` declares the pages and A/B tests, checks them when the server
  starts, and serves them.
- `docs.go` renders the docs: the README's list is the sidebar, links between
  pages are rewritten to their paths here, and `llms.txt` names the pages
  here. `just run` reads a checkout beside this repository.
- `site/` is embedded into the binary: `pages/` (one template per page, such
  as `home.html`), `partials/` (the request form and the footer), `requested.html`,
  `docs.html`, `site.css`, `site.js`, the fonts and the favicon. The work
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
attributes on `<html>` as `home.html` does.

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
