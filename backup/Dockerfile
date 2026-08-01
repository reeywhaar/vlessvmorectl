# The backup sidecar: archives the panel's data directory and forwards it to backio.
#
# Built from the repo root, so it can see go.mod:
#   docker build -f backup/Dockerfile -t ghcr.io/reeywhaar/vlessvmorectl-backup .
#
# A separate image rather than a second command in the main one: it wants its own restart
# cadence, and the panel image gains neither 7zip nor a backio credential.
ARG GO_VERSION=1.26

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
# No `go mod download`: backup/ imports only the standard library, so nothing in go.sum has
# to be fetched. go.mod is copied all the same — without it there is no module.
COPY go.mod go.sum ./
COPY backup ./backup
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags "-s -w -buildid=" -o /out/backup ./backup

# 7-Zip's own static build rather than `apk add 7zip`, which is 4.8 MB: the packaged 7zz links
# libstdc++, and that shared library alone is 2.8 MB of an image whose Go binary is 6.4. This is
# the same program producing the same AES-256 zip — nothing about restoring changes.
#
# Pinned by checksum, which is the part that earns the swap. An apk package arrives signed; a
# tarball from a web server does not, so the digest is the only thing standing between a
# compromised mirror and a binary that handles the backup password. Bump both together.
FROM --platform=$BUILDPLATFORM alpine:latest AS sevenzip
ARG TARGETARCH
ARG SEVENZIP=7z2501
RUN apk add --no-cache curl tar xz
RUN case "$TARGETARCH" in \
      amd64) arch=x64;   sha=4ca3b7c6f2f67866b92622818b58233dc70367be2f36b498eb0bdeaaa44b53f4 ;; \
      arm64) arch=arm64; sha=39c5140f02ce4436599303c59a149f654cb1bbc47cdc105a942120d747ae040d ;; \
      *) echo "no 7-Zip build pinned for $TARGETARCH" >&2; exit 1 ;; \
    esac && \
    curl -fsSL -o /tmp/7z.tar.xz "https://www.7-zip.org/a/${SEVENZIP}-linux-${arch}.tar.xz" && \
    echo "${sha}  /tmp/7z.tar.xz" | sha256sum -c - && \
    xz -dc /tmp/7z.tar.xz | tar -x -C /tmp 7zzs && \
    install -D -m 0755 /tmp/7zzs /out/7zzs

FROM alpine:latest
# ca-certificates because BACKIO_URL may be https.
RUN apk add --no-cache ca-certificates

# 7zzs is the static one; the 7zz in the same tarball is glibc-linked and will not run here.
# Symlinked so main.go's plain "7z" resolves, as it did with the package.
COPY --from=sevenzip /out/7zzs /usr/local/bin/7zz
RUN ln -s 7zz /usr/local/bin/7z

COPY --from=builder /out/backup /backup
COPY --chmod=0755 backup/backup_loop.sh /backup_loop.sh

# No `VOLUME /backups`, and the directory is deliberately not created here either. Docker
# creates a mount target the image is missing, so an existing /backups means a volume is mounted
# and the sidecar keeps local copies; no directory means it uploads and keeps none. `VOLUME`
# would break that by conjuring an anonymous volume nobody named and nothing prunes.
CMD ["/backup_loop.sh"]
