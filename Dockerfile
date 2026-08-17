# syntax=docker/dockerfile:1
#
# mailcow dockerapi
#
# The Python service needed a base image with the docker and aiodocker clients,
# psutil, FastAPI, uvicorn and an entrypoint script that called openssl to create
# the server certificate. All of that is in the binary now, so the runtime image
# carries the binary, a CA bundle and the timezone database, and nothing else.

FROM golang:1.26.6-alpine AS build

WORKDIR /src

# Copy the manifests first so the dependency layer is cached across source edits.
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY cmd/ ./cmd/
COPY internal/ ./internal/

# VERSION is the only way the version reaches the binary: `git describe` cannot
# run here, because .dockerignore keeps .git out of the build context. The CI
# passes the tag; a plain `docker build` yields "dev", which is honest.
ARG VERSION=dev
ARG BUILD_DATE=unknown
ENV CGO_ENABLED=0 GOOS=linux

# ${VERSION:-dev} guards against an explicitly empty --build-arg VERSION=, which
# would stamp an empty string and read as a broken build rather than an unstamped
# one.
RUN go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION:-dev} -X main.buildDate=${BUILD_DATE:-unknown}" \
      -o /out/dockerapi ./cmd/dockerapi


# The runtime stage carries no shell and no package manager.
FROM gcr.io/distroless/static-debian13:latest

LABEL org.opencontainers.image.title="mailcow-dockerapi" \
      org.opencontainers.image.description="Broker between the mailcow UI and the Docker daemon" \
      org.opencontainers.image.source="https://github.com/mailcow/mailcow-dockerized"

COPY --from=build /out/dockerapi /dockerapi

# The image runs as root, as the Python image did: the service talks to
# /var/run/docker.sock, which is owned by root:docker on the host, and it writes
# its certificate pair to /app. Switching to the :nonroot tag is possible, but
# only together with a matching group on the socket mount.

# The service creates its own certificate at startup; openssl and the entrypoint
# script of the Python implementation are gone.
EXPOSE 443

# The observability endpoint. Unlike 443 it speaks plain HTTP and belongs on an
# internal network only.
EXPOSE 9394

ENTRYPOINT ["/dockerapi"]
