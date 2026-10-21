# syntax=docker/dockerfile:1
# bull-Tunnel control panel.
#
# The panel is the control plane: it talks to the managed servers over SSH and
# to the DNS providers over HTTPS. It never carries user traffic, so the image
# stays small and the container can be replaced at any time.
#
# The image holds the panel (the API, the web interface) and the tunnel engine
# (the program that runs on both ends of a tunnel).
FROM golang:1.27-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/bull-tunnel-panel ./cmd/bull-tunnel-panel && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/bull-tunnel-engine ./cmd/bull-tunnel-engine

FROM debian:bookworm-slim

LABEL org.opencontainers.image.title="bull-Tunnel" \
      org.opencontainers.image.description="Control panel for reverse tunnels: health checks, automatic failover, DNS switching" \
      org.opencontainers.image.source="https://github.com/Liwyd/bull-Tunnel" \
      org.opencontainers.image.licenses="MIT"

ENV PATH="/opt/bull-tunnel/bin:${PATH}" \
    BT_DATA_DIR=/data \
    BT_HOST=0.0.0.0 \
    BT_PORT=8080 \
    BT_STATIC_DIR=/opt/bull-tunnel/static

RUN useradd --system --uid 10001 --create-home --home-dir /home/bulltunnel bulltunnel
COPY --from=build /out/bull-tunnel-panel /opt/bull-tunnel/bin/
COPY --from=build /out/bull-tunnel-engine /opt/bull-tunnel/bin/
COPY bulltunnel/static /opt/bull-tunnel/static
RUN mkdir -p /data && chown bulltunnel:bulltunnel /data

USER bulltunnel
VOLUME ["/data"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/opt/bull-tunnel/bin/bull-tunnel-panel", "--check"]

ENTRYPOINT ["/opt/bull-tunnel/bin/bull-tunnel-panel"]
