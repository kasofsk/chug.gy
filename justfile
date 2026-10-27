# The site's own commands.

run:
    CHUG_SITE_DATA=./dist/data go run .

test:
    gofmt -l . && go vet ./... && go test ./...

image:
    docker build -t chug.gy .
