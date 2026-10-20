# syntax=docker/dockerfile:1
# Bull-Tunn control panel.
#
# The panel is the control plane: it talks to the managed servers over SSH and
# to the DNS providers over HTTPS. It never carries user traffic, so the image
# stays small and the container can be replaced at any time.
FROM python:3.12-slim AS build

WORKDIR /src
RUN python -m venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"
COPY pyproject.toml README.md LICENSE ./
COPY bulltunn ./bulltunn
RUN pip install --no-cache-dir --upgrade pip \
 && pip install --no-cache-dir . \
 && pip install --no-cache-dir --upgrade build \
 && python -m build --wheel --outdir /dist

FROM python:3.12-slim AS runtime

LABEL org.opencontainers.image.title="Bull-Tunn" \
      org.opencontainers.image.description="Control panel for reverse tunnels: health checks, automatic failover, DNS switching" \
      org.opencontainers.image.source="https://github.com/Liwyd/Bull-Tunn" \
      org.opencontainers.image.licenses="MIT"

ENV PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PATH="/opt/venv/bin:$PATH" \
    BT_DATA_DIR=/data \
    BT_HOST=0.0.0.0 \
    BT_PORT=8080

RUN useradd --system --uid 10001 --create-home --home-dir /home/bulltunn bulltunn
COPY --from=build /opt/venv /opt/venv
WORKDIR /opt/bulltunn
RUN mkdir -p /data && chown bulltunn:bulltunn /data

USER bulltunn
VOLUME ["/data"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD python -c "import urllib.request as u; u.urlopen('http://127.0.0.1:8080/api/session', timeout=4)" || exit 1

ENTRYPOINT ["bulltunn"]
CMD ["serve"]
