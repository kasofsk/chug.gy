# One static binary, in an image that holds nothing else: no shell, no
# libc, no certificates, because the server opens no connection of its own.
#
# The image holds no docs. Where it runs, CHUG_SITE_DOCS names a directory
# holding kasofsk/chuggy-docs (on chug.gy, the Deployment in gdoteof/chuggy-fabric
# fills it from the docs image); unset, /docs and /llms.txt go to GitHub.

FROM golang:1.27-bookworm@sha256:ce59b41415e2524ae99926fe1f37ce48ba8c194dcbd2a56a65436cdcefa2bd34 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY site ./site
RUN CGO_ENABLED=0 GOFLAGS=-trimpath go build -ldflags='-s -w' -o /chug.gy .

FROM scratch
COPY --from=build /chug.gy /chug.gy
# 65532 is the conventional nonroot uid; the manifest states it as well.
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/chug.gy"]
