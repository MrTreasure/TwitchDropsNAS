FROM node:26.0.0-slim@sha256:ccd1c33b2876c07564b3fae7f6a5815aa42f71163faf07d00a9907e398d48bdc AS ui-build
WORKDIR /build
ADD https://registry.npmjs.org/pnpm/-/pnpm-12.3.4.tgz /tmp/pnpm.tgz
RUN mkdir /opt/pnpm && tar -xzf /tmp/pnpm.tgz -C /opt/pnpm --strip-components=1
COPY ui/package.json ui/pnpm-lock.yaml ./
RUN node /opt/pnpm/bin/pnpm.mjs install --frozen-lockfile --ignore-scripts
COPY ui/ ./
RUN chmod 0755 /opt/pnpm/bin/pnpm.mjs && ln -s /opt/pnpm/bin/pnpm.mjs /usr/local/bin/pnpm
RUN node /opt/pnpm/bin/pnpm.mjs run build

FROM python:3.11-slim@sha256:0a236bf8f0b6013383df04d1cd02cb0abe5fbfa1a7012141d7ff9202b7cb30a6
WORKDIR /app
COPY --chmod=0444 requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt && mkdir -p /data /run/twitch-drops && chown 1000:1000 /data /run/twitch-drops
COPY --chown=1000:1000 --chmod=0444 service.py twitch.py goals.py transport.py storage.py network_proxy.py webui.py ./
COPY --from=ui-build --chown=1000:1000 /build/dist ./ui
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 TDM_DATA_DIR=/data TDM_SOCKET=/run/twitch-drops/service.sock TDM_HTTP_PORT=8080
COPY --chown=1000:1000 --chmod=0444 docs/openapi.json ./docs/openapi.json
COPY --chown=1000:1000 --chmod=0444 catalogue/announcements.json ./catalogue/announcements.json
RUN chmod -R a+rX /app/ui && chmod 0755 /app/docs /app/catalogue
USER 1000:1000
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD python -c "import urllib.request; assert urllib.request.urlopen('http://127.0.0.1:8080/health',timeout=3).status==200"
CMD ["python", "service.py"]
