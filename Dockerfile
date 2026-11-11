# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
# One Dockerfile, three images:
#   app:    serve, worker, migrate, admin (distroless, non-root)
#   runner: browser runner on headless-shell (Chromium with sandbox, non-root)
#   egress: smokescreen egress proxy (blocks private networks and cloud metadata)

FROM --platform=$BUILDPLATFORM golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=""
ARG DATE=""
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-trimpath
WORKDIR /src
COPY go.mod go.sum ./
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
      -ldflags="-s -w -X github.com/1xxn/getafjob/internal/platform/buildinfo.Version=${VERSION} -X github.com/1xxn/getafjob/internal/platform/buildinfo.Commit=${COMMIT} -X github.com/1xxn/getafjob/internal/platform/buildinfo.Date=${DATE}" \
      -o /out/getafjob ./cmd/getafjob
# Empty blob directory, owned by the non-root user in the app image (named volumes copy its ownership).
RUN mkdir -p /out/blobs

# smokescreen comes from the build module in deploy/egress: a pinned release with dependencies raised to
# versions without known vulnerabilities (go.sum verified, Renovate-managed).
FROM --platform=$BUILDPLATFORM golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9 AS smokescreen
ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-trimpath
WORKDIR /egress
COPY deploy/egress/go.mod deploy/egress/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-s -w" -o /out/smokescreen github.com/stripe/smokescreen

FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329 AS app
COPY --from=build /out/getafjob /usr/local/bin/getafjob
COPY --from=build --chown=65532:65532 /out/blobs /var/lib/getafjob/blobs
USER 65532:65532
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/getafjob"]
CMD ["serve"]

FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329 AS egress
COPY --from=smokescreen /out/smokescreen /usr/local/bin/smokescreen
# getafjob only serves as the health check client (no shell, no curl in distroless).
COPY --from=build /out/getafjob /usr/local/bin/getafjob
USER 65532:65532
EXPOSE 4750
ENTRYPOINT ["/usr/local/bin/smokescreen"]
# Private, loopback, link-local (incl. 169.254.169.254) and other special ranges are denied by default;
# the extra ranges below close the rest (CGNAT, benchmarking, multicast, reserved).
CMD ["--listen-ip=0.0.0.0", "--listen-port=4750", "--timeout=15s", \
     "--deny-range=0.0.0.0/8", "--deny-range=100.64.0.0/10", "--deny-range=192.0.0.0/24", \
     "--deny-range=198.18.0.0/15", "--deny-range=224.0.0.0/4", "--deny-range=240.0.0.0/4", \
     "--deny-range=fc00::/7", "--deny-range=fe80::/10", "--deny-range=ff00::/8", \
     "--additional-error-message-on-deny=blocked by the getafjob egress policy"]

FROM chromedp/headless-shell:158.0.8117.2@sha256:8ffed8e29a6427515410a54a93dee50b0ec3f206095233dc374e113e9324cc7a AS runner
USER root
# The upstream image ships locales readable by root only; the runner user needs them. Upgrade any packages with
# Debian security updates before removing apt, so the runner needs no package manager at run time and has no
# unpatched OS libraries or apt advisories in the image scan.
RUN chmod -R a+rX /headless-shell && \
    apt-get update && apt-get -y --only-upgrade install zlib1g && rm -rf /var/lib/apt/lists/* && \
    dpkg --purge --force-depends apt libapt-pkg7.0 && \
    groupadd --gid 10001 runner && \
    useradd --uid 10001 --gid 10001 --no-create-home --home-dir /tmp --shell /usr/sbin/nologin runner
COPY --from=build /out/getafjob /usr/local/bin/getafjob
ENV GETAFJOB_BROWSER_BIN=/headless-shell/headless-shell HOME=/tmp GETAFJOB_HEALTH_ADDR=:8081
USER 10001:10001
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/getafjob"]
CMD ["runner"]
