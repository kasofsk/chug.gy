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
| `CHUG_SITE_DOCS` | A checkout of [kasofsk/chuggy-docs](https://github.com/kasofsk/chuggy-docs), rendered at `/docs` with `llms.txt` beside it. Unset, both go to GitHub. The image sets it. |

```sh
go test ./...
docker build -t chug.gy .
```

## What is where

- `main.go` is the server: the routes, the form's checks, the file, the
  policy headers and the redirects for short paths such as `/linux`.
- `docs.go` renders the docs: the README's list is the sidebar, links between
  pages are rewritten to their paths here, and `llms.txt` names the pages
  here. The `Dockerfile` pins the docs commit; `just run` reads a checkout
  beside this repository.
- `site/` is embedded into the binary: `index.html`, `requested.html`,
  `site.css`, `site.js`, `story.js`, the fonts and the favicon.
- `story.js` is the drawing beside the page: a cluster of chuggy's own
  tickets, the machines their jobs run on, and the scenes that change its
  state as each section is read. The tickets, the machines and the scenes
  are the tables at the top of the file; `story.play()` in the console runs
  the scenes on a clock, for a recording.

## Reading the requests

Each line of `requests.jsonl` is one request: `at`, `email`, `name`, `github`,
`interest` (`use`, `partner`, `invest` or `other`) and `about`. With a token:

```sh
curl -H "Authorization: Bearer $TOKEN" https://chug.gy/admin/requests
```
