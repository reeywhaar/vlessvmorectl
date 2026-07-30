ARG GO_VERSION=1.26
# node:current-alpine rather than a pinned major: the bundle is plain ES modules and depends
# on no Node API that moves between releases. Pin it (e.g. node:26-alpine) if one ever does.
ARG NODE_IMAGE=node:current-alpine

# ---------------------------------------------------------------------------
# The SPA.
#
# --platform=$BUILDPLATFORM pins this stage to the machine doing the building: the bundle is
# architecture-independent, so there is no reason to run npm under QEMU.
# ---------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS web
WORKDIR /src/web
# The lockfile alone first, so `npm ci` is cached until a dependency actually changes.
COPY web/package.json web/package-lock.json ./
RUN npm ci
# Both HTML entries: the panel and the subscriber share page are separate Vite entries, so
# the operator's pages are not in the module graph a stranger's browser loads. vite.config.ts
# names both, so a missing one is a hard error rather than a quietly smaller build.
COPY web/tsconfig.json web/vite.config.ts web/index.html web/access.html ./
COPY web/public ./public
COPY web/src ./src
RUN npm run build
# Fail here, not in production. An empty build is otherwise invisible until somebody loads
# the panel and gets the placeholder page, or opens a share link and gets the panel's shell.
RUN test -s dist/index.html && test -s dist/access.html

# Gzip the bundle before it is embedded, replacing each original: ~480 KB off the binary, and
# the compressed bytes are then served from memory rather than recompressed per request.
# internal/api/spa.go registers a "foo.js.gz" under "/foo.js", so nothing downstream knows.
#
# Text only, since gzipping a .webp makes it bigger. -9 because this runs once at build time.
RUN find dist -type f \( -name '*.js' -o -name '*.css' -o -name '*.html' -o -name '*.svg' -o -name '*.json' -o -name '*.map' \) \
      -exec gzip -9 {} + && \
    echo "embedded bundle: $(du -sh dist | cut -f1)"

# ---------------------------------------------------------------------------
# The binary.
# ---------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# Copied path by path rather than `COPY . .` with a .dockerignore: an allowlist cannot
# accidentally admit a .env holding every node's bearer token, a local data/ directory with
# admins.json in it, or web/node_modules.
COPY main.go ./
COPY internal ./internal
COPY web/embed.go ./web/embed.go
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags "-s -w -buildid= -X vlessvmorectl/internal/cli.Version=${VERSION}" \
      -o /out/vlessvmorectl .

# ---------------------------------------------------------------------------
# Runtime.
#
# The only stage that runs on the target architecture, and it installs two packages — so a
# multi-platform build emulates one short apk step rather than a compile.
# ---------------------------------------------------------------------------
FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata

COPY --from=build /out/vlessvmorectl /usr/local/bin/vlessvmorectl

# argv[0] dispatch, so `docker exec vlessvmorectl users add alice` works as well as the
# canonical `docker exec vlessvmorectl vlessvmorectl users add alice`.
RUN ln -s vlessvmorectl /usr/local/bin/users

# admins.json lives here. Mount a volume over it, or every administrator disappears when
# this container is replaced and the only way back in is a shell on the host.
VOLUME /var/lib/vlessvmorectl

# Documentation only. The listen port is not configurable; remap it with -p.
EXPOSE 80

# Runs our own binary rather than shelling out to wget, so the image needs no HTTP client.
# A loopback GET exercises the listener, the router and the handler chain, so a process that
# is running but wedged fails it.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["vlessvmorectl", "healthcheck"]

# Split so `docker compose up` runs the daemon while
# `docker run --rm IMAGE users add alice` replaces the command rather than appending to it.
ENTRYPOINT ["vlessvmorectl"]
CMD ["serve"]
