ARG GO_VERSION=1.26
# node:current-alpine tracks the newest release line rather than pinning a major, which
# is what this project wants: the bundle is plain ES modules and nothing here depends on
# a Node API that moves between releases. If a future Node ever does break the build,
# pin it here (e.g. NODE_IMAGE=node:26-alpine) rather than debugging it in CI.
ARG NODE_IMAGE=node:current-alpine
ARG ALPINE_VERSION=3.22

# ---------------------------------------------------------------------------
# The SPA.
#
# --platform=$BUILDPLATFORM pins this stage to the machine doing the building. The bundle
# is architecture-independent, so there is never a reason to run npm under QEMU.
# ---------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS web
WORKDIR /src/web
# The lockfile alone first, so `npm ci` is cached until a dependency actually changes.
COPY web/package.json web/package-lock.json ./
RUN npm ci
# Both HTML entries. The panel and the subscriber share page are separate Vite entries —
# separate bundles, so the operator's pages are not in the module graph a stranger's
# browser loads — and vite.config.ts names both, so a missing one is a hard
# "Cannot resolve entry module" rather than a quietly smaller build.
COPY web/tsconfig.json web/vite.config.ts web/index.html web/access.html ./
COPY web/public ./public
COPY web/src ./src
RUN npm run build
# Fail here, not in production. A build that emits nothing is otherwise invisible until
# somebody loads the panel and gets the placeholder page — and one that emits only the
# panel is invisible until somebody opens a share link and gets the panel's shell.
RUN test -s dist/index.html && test -s dist/access.html

# Gzip the bundle before it is embedded, replacing each original.
#
# Worth doing twice over: it takes roughly 480 KB off the binary — the bundle is otherwise
# the largest single thing in it after the Go runtime — and the compressed bytes are then
# served straight from memory instead of being recompressed for every request. internal/
# api/spa.go registers a "foo.js.gz" under "/foo.js", so nothing downstream, including
# the bundle's own asset references, knows the difference.
#
# -9 because this runs once at build time and the output ships in an image; the extra
# seconds are free and the bytes are not. Only text is compressed: gzipping a .webp or a
# .png makes it bigger.
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
# accidentally admit a .env holding every node's bearer token, a local data/ directory
# with admins.json in it, or web/node_modules.
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
# The only stage that runs on the target architecture, and it does nothing but install
# two packages — so a multi-platform build emulates one short apk step rather than a
# compile.
# ---------------------------------------------------------------------------
FROM alpine:${ALPINE_VERSION}
RUN apk add --no-cache ca-certificates tzdata

COPY --from=build /out/vlessvmorectl /usr/local/bin/vlessvmorectl

# argv[0] dispatch, so `docker exec vlessvmorectl users add alice` works as well as the
# canonical `docker exec vlessvmorectl vlessvmorectl users add alice`.
RUN ln -s vlessvmorectl /usr/local/bin/users

# admins.json lives here. Mount a named volume or a bind mount over it, or every
# administrator disappears the moment this container is replaced — and the only way back
# in is a shell on the host.
VOLUME /var/lib/vlessvmorectl

# Documentation only. The listen port is not configurable; remap it with -p.
EXPOSE 80

# Runs our own binary rather than shelling out to wget, so the image needs no HTTP client
# and the check is exec-form. A loopback GET exercises the listener, the router and the
# handler chain, so a process that is running but wedged fails it.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["vlessvmorectl", "healthcheck"]

# Split so `docker compose up` runs the daemon while
# `docker run --rm IMAGE users add alice` replaces the command rather than appending to it.
ENTRYPOINT ["vlessvmorectl"]
CMD ["serve"]
