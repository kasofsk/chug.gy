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
go run . 
```

Serves on `:8080` with the form turned off. The environment configures it:

| Variable | Meaning |
| --- | --- |
| `CHUG_SITE_LISTEN` | The address to listen on. Default `:8080`. |
| `CHUG_SITE_DATA` | The directory `requests.jsonl` is kept in. Unset, the form answers 503. |
| `CHUG_SITE_ADMIN_TOKEN_FILE` | A file holding the bearer token `GET /admin/requests` takes. Unset, the route is 404. |
| `CHUG_SITE_HOST` | The canonical host. A request for another host is redirected to it, except on loopback and for `/healthz`. |

```sh
go test ./...
docker build -t chug.gy .
```

## What is where

- `main.go` is the server: the routes, the form's checks, the file, the
  policy headers and the redirects for short paths such as `/docs`.
- `site/` is embedded into the binary: `index.html`, `requested.html`,
  `site.css`, `site.js`, the fonts and the favicon. The work graph in the
  hero is inline SVG in `index.html`.

## Reading the requests

Each line of `requests.jsonl` is one request: `at`, `email`, `name`, `github`,
`interest` (`use`, `partner`, `invest` or `other`) and `about`. With a token:

```sh
curl -H "Authorization: Bearer $TOKEN" https://chug.gy/admin/requests
```
