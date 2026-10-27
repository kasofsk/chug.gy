# One static binary, in an image that holds nothing else: no shell, no
# libc, no certificates, because the server opens no connection of its own.
FROM golang:1.24-bookworm@sha256:1a6d4452c65dea36aac2e2d606b01b4a029ec90cc1ae53890540ce6173ea77ac AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
COPY site ./site
RUN CGO_ENABLED=0 GOFLAGS=-trimpath go build -ldflags='-s -w' -o /chug.gy .

FROM scratch
COPY --from=build /chug.gy /chug.gy
# 65532 is the conventional nonroot uid; the manifest states it as well.
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/chug.gy"]
