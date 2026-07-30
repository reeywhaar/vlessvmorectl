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

FROM alpine:latest
# ca-certificates because BACKIO_URL may be https; 7zip only for BACKUP_PASSWORD.
RUN apk add --no-cache ca-certificates 7zip

COPY --from=builder /out/backup /backup
COPY --chmod=0755 backup/backup_loop.sh /backup_loop.sh

# Local copies, rotated by the same policy as the remote. Mount a volume here for backups
# that survive the remote being unreachable.
VOLUME /backups

CMD ["/backup_loop.sh"]
