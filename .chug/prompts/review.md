Hold this change to the ticket and to this repository: one static Go binary that embeds `site/` and serves the docs from `CHUG_SITE_DOCS`.

- It does what the ticket asks and nothing adjacent; no unrelated edits.
- `gofmt`, `go vet ./...` and `go test ./...` pass, and new behaviour has a test.
- Landing-page copy reads well for non-technical readers first and is true.
- The README's tables and "What is where" still match the code.
- No secret is committed; the admin token is created out of band.
