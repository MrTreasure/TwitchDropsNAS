FROM node:26.0.0-slim@sha256:ccd1c33b2876c07564b3fae7f6a5815aa42f71163faf07d00a9907e398d48bdc AS ui-build
WORKDIR /build
ADD https://registry.npmjs.org/pnpm/-/pnpm-12.3.4.tgz /tmp/pnpm.tgz
RUN mkdir /opt/pnpm && tar -xzf /tmp/pnpm.tgz -C /opt/pnpm --strip-components=1
COPY ui/package.json ui/pnpm-lock.yaml ./
RUN node /opt/pnpm/bin/pnpm.mjs install --frozen-lockfile --ignore-scripts
COPY ui/ ./
RUN chmod 0755 /opt/pnpm/bin/pnpm.mjs && ln -s /opt/pnpm/bin/pnpm.mjs /usr/local/bin/pnpm
RUN node /opt/pnpm/bin/pnpm.mjs run build

FROM golang:1.26.5@sha256:705e964a93a2fd2e75c7d59bb7d781b57e30f12293ffde5175c69229e18fb678 AS backend-build
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY catalogue/*.json ./catalogue/
COPY docs/openapi.json ./docs/openapi.json
COPY --from=ui-build /build/dist ./ui/dist
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o /twitch-drops .

FROM alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8
RUN mkdir -p /data /run/twitch-drops && chown 1000:1000 /data /run/twitch-drops
COPY --from=backend-build --chmod=0555 /twitch-drops /app/twitch-drops
ENV TDM_DATA_DIR=/data TDM_SOCKET=/run/twitch-drops/service.sock TDM_HTTP_HOST=0.0.0.0 TDM_HTTP_PORT=8080
COPY --chmod=0444 LICENSE NOTICE.md THIRD_PARTY_NOTICES.md /app/
USER 1000:1000
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD ["/app/twitch-drops", "health"]
ENTRYPOINT ["/app/twitch-drops"]
CMD ["serve"]
