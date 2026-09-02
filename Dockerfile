# syntax=docker/dockerfile:1

# --- build stage -------------------------------------------------------------
# Pin the build stage to the runner's native arch ($BUILDPLATFORM) and
# cross-compile to the requested target ($TARGETOS/$TARGETARCH) so a
# multi-platform build stays native-speed instead of emulating each arch.
FROM --platform=$BUILDPLATFORM golang:1.25 AS build

WORKDIR /src

# Warm the module cache on just the manifests, so code changes don't refetch.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

# Static binary so it runs on a distroless/scratch base with no libc.
ARG VERSION=docker
ARG COMMIT=none
ARG DATE=unknown
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE} -X main.builtBy=docker" \
    -o /out/cloudemu ./cmd/cloudemu

# --- runtime stage -----------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/cloudemu /usr/local/bin/cloudemu

# AWS (LocalStack-compatible), Azure (HTTPS), GCP, Kubernetes data-plane, OCI.
# OCI (4571) is opt-in (--providers …,oci) so it isn't published by default, but
# the port is declared here for when it is enabled.
EXPOSE 4566 4568 4569 4570 4571

# Bind all interfaces so the container is reachable from the host / other
# containers. The control plane (/_cloudemu/reset) is on by default — see the
# docs for --admin=false if you don't want it exposed.
ENTRYPOINT ["cloudemu"]
CMD ["serve", "--host", "0.0.0.0"]
