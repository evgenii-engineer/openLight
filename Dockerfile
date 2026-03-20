# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN mkdir -p /out/data /out/etc/openlight /out/tmp/openlight && \
    cp configs/agent.container.default.yaml /out/etc/openlight/agent.yaml && \
    target_os="${TARGETOS:-$(go env GOOS)}" && \
    target_arch="${TARGETARCH:-$(go env GOARCH)}" && \
    CGO_ENABLED=0 GOOS="${target_os}" GOARCH="${target_arch}" \
    go build -trimpath -ldflags="-s -w" -o /out/openlight-agent ./cmd/agent

FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED

LABEL org.opencontainers.image.title="openLight" \
      org.opencontainers.image.description="Tiny AI control plane for personal infrastructure" \
      org.opencontainers.image.source="https://github.com/evgenii-engineer/openLight" \
      org.opencontainers.image.url="https://github.com/evgenii-engineer/openLight" \
      org.opencontainers.image.documentation="https://github.com/evgenii-engineer/openLight/blob/master/README.md" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}"

WORKDIR /var/lib/openlight

COPY --from=builder --chown=65532:65532 /out/openlight-agent /usr/local/bin/openlight-agent
COPY --from=builder --chown=65532:65532 /out/data /var/lib/openlight/data
COPY --from=builder --chown=65532:65532 /out/etc/openlight /etc/openlight
COPY --from=builder --chown=65532:65532 /out/tmp/openlight /tmp/openlight

VOLUME ["/etc/openlight", "/var/lib/openlight/data"]

EXPOSE 8081

ENTRYPOINT ["/usr/local/bin/openlight-agent"]
