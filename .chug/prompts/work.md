You are changing chug.gy, the project's website: one static Go binary (`main.go`, `docs.go`) that embeds `site/` and renders the docs directory named by `CHUG_SITE_DOCS`.

- Read the code before changing it, and change only what the ticket asks.
- The copy on the landing page is for non-technical readers first; keep it plain and true.
- The README's tables (environment, "What is where") must stay true when you change what they describe.
- Run `test -z "$(gofmt -l .)" && go vet ./... && go test ./...` before you finish.
- Never commit a secret. The admin token is created out of band.
