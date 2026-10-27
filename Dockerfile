# One static binary, in an image that holds nothing else: no shell, no
# libc, no certificates, because the server opens no connection of its own.
#
# The docs are kasofsk/chuggy-docs at the commit below, fetched at build and
# rendered by the binary at start. Moving the docs forward is moving the
# commit and building again.
ARG CHUGGY_DOCS_COMMIT=d2c73a0c31d8ae171ced4d6acc3575244be2732d

FROM golang:1.24-bookworm@sha256:1a6d4452c65dea36aac2e2d606b01b4a029ec90cc1ae53890540ce6173ea77ac AS build
ARG CHUGGY_DOCS_COMMIT
ADD https://github.com/kasofsk/chuggy-docs/archive/${CHUGGY_DOCS_COMMIT}.tar.gz /tmp/docs.tar.gz
RUN mkdir /docs && tar -xzf /tmp/docs.tar.gz -C /docs --strip-components=1 \
 && test -f /docs/docs/index.md && test -f /docs/llms.txt && test -f /docs/README.md
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY site ./site
RUN CGO_ENABLED=0 GOFLAGS=-trimpath go build -ldflags='-s -w' -o /chug.gy .

FROM scratch
COPY --from=build /chug.gy /chug.gy
COPY --from=build /docs/docs /docs/docs
COPY --from=build /docs/README.md /docs/llms.txt /docs/
ENV CHUG_SITE_DOCS=/docs
# 65532 is the conventional nonroot uid; the manifest states it as well.
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/chug.gy"]
