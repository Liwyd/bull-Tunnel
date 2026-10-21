# syntax=docker/dockerfile:1
# bull-Tunnel control panel.
#
# The panel is the control plane: it talks to the managed servers over SSH and
# to the DNS providers over HTTPS. It never carries user traffic, so the image
# stays small and the container can be replaced at any time.
FROM python:3.12-slim AS build

WORKDIR /src
RUN python -m venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"
COPY pyproject.toml README.md LICENSE ./
COPY bulltunnel ./bulltunnel
RUN pip install --no-cache-dir --upgrade pip \
 && pip install --no-cache-dir . \
 && pip install --no-cache-dir --upgrade build \
 && python -m build --wheel --outdir /dist

FROM python:3.12-slim AS runtime

LABEL org.opencontainers.image.title="bull-Tunnel" \
      org.opencontainers.image.description="Control panel for reverse tunnels: health checks, automatic failover, DNS switching" \
      org.opencontainers.image.source="https://github.com/Liwyd/bull-Tunnel" \
      org.opencontainers.image.licenses="MIT"

ENV PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PATH="/opt/venv/bin:$PATH" \
    BT_DATA_DIR=/data \
    BT_HOST=0.0.0.0 \
    BT_PORT=8080

RUN useradd --system --uid 10001 --create-home --home-dir /home/bulltunnel bulltunnel
COPY --from=build /opt/venv /opt/venv
WORKDIR /opt/bull-tunnel
RUN mkdir -p /data && chown bulltunnel:bulltunnel /data

USER bulltunnel
VOLUME ["/data"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD python -c "import urllib.request as u; u.urlopen('http://127.0.0.1:8080/api/session', timeout=4)" || exit 1

ENTRYPOINT ["python", "-m", "bulltunnel"]
CMD ["serve"]
