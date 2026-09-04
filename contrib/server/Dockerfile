# syntax=docker/dockerfile:1

# Batteries-included CloudEmu server: the standalone wire server with real
# data-plane engines wired in. Build from the REPOSITORY ROOT so the module's
# `replace` directives (../.., ../realengine, ../dockerengine) resolve:
#
#   docker build -f contrib/server/Dockerfile -t cloudemu-server .

# --- build stage -------------------------------------------------------------
# Pin the build stage to the runner's native arch ($BUILDPLATFORM) and
# cross-compile to the requested target ($TARGETOS/$TARGETARCH) so a
# multi-platform build stays native-speed instead of emulating each arch.
FROM --platform=$BUILDPLATFORM golang:1.25 AS build

WORKDIR /src

# The whole repo is needed: contrib/server replaces the core module and the two
# engine modules by relative path.
COPY . .

# embedded-postgres runs a real (glibc) Postgres binary at runtime, so keep CGO
# off for a portable Go binary but target a glibc runtime base below.
ARG VERSION=docker
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd contrib/server && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/cloudemu-server .

# --- runtime stage -----------------------------------------------------------
# Debian (glibc) so embedded-postgres' binaries run; the docker CLI is copied in
# so the Docker-backed engines (--db=mysql|both, --compute=docker,
# --containers=docker) can shell out. Mount the host's docker socket to use them.
# python3 + nodejs are the interpreters the subprocess functions engine execs
# (contrib/realengine/functions/functions_runtime.go) — without them
# --functions=subprocess would be shipped but unusable.
FROM debian:12-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates python3 nodejs \
    && rm -rf /var/lib/apt/lists/*

COPY --from=docker:27-cli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=build /out/cloudemu-server /usr/local/bin/cloudemu-server

# Engine selection is env-driven (flags override). All default to off, so the
# image behaves like a plain in-memory `cloudemu serve` until you opt in.
ENV CLOUDEMU_DB=off \
    CLOUDEMU_CACHE=off \
    CLOUDEMU_FUNCTIONS=off \
    CLOUDEMU_COMPUTE=off \
    CLOUDEMU_CONTAINERS=off \
    CLOUDEMU_STORAGE=off

# AWS (LocalStack-compatible), Azure (HTTPS), GCP, Kubernetes (4570), OCI (4571).
EXPOSE 4566 4568 4569 4570 4571

# Bind all interfaces so the container is reachable from the host.
ENTRYPOINT ["cloudemu-server"]
CMD ["--host", "0.0.0.0"]
