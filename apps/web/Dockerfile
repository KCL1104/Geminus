# The web image: Next.js server with the browser session's BFF (ADR-018,
# T-039; docs/runbooks/deploy.md). Built from the repository root:
# docker build -f apps/web/Dockerfile -t geminus-web .
#
# The base image is pinned by digest (ADR-011) at apps/web/.nvmrc's Node.
# The build installs from the lockfile (npm ci) and builds Next's standalone
# server (GEMINUS_NEXT_STANDALONE=1, next.config.ts); the runtime has only
# that server and its static files, runs as the image's non-root user
# "node" (uid 1000) and has no npm. Nothing secret is built in: the OIDC
# client secret and SESSION_KEYS come from the platform's environment at
# run time (no build argument is declared, so the platform cannot pass one
# into the build).

FROM node:24.21.0-trixie-slim@sha256:173f125896c3b47ddf056734c7ea789d04595a6a08769a8f78e0df642781fb66 AS build
WORKDIR /app
ENV NEXT_TELEMETRY_DISABLED=1
COPY apps/web/package.json apps/web/package-lock.json apps/web/.npmrc apps/web/.nvmrc ./
RUN npm ci --no-audit --no-fund
COPY apps/web/ ./
ENV GEMINUS_NEXT_STANDALONE=1
RUN npm run build

FROM node:24.21.0-trixie-slim@sha256:173f125896c3b47ddf056734c7ea789d04595a6a08769a8f78e0df642781fb66
# The server needs node only: no package manager at run time.
RUN rm -rf /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/corepack /usr/local/bin/npm /usr/local/bin/npx \
      /usr/local/bin/corepack /opt/yarn-* /usr/local/bin/yarn /usr/local/bin/yarnpkg
ENV NODE_ENV=production NEXT_TELEMETRY_DISABLED=1 PORT=3000 HOSTNAME=0.0.0.0
WORKDIR /app
# Root-owned and read-only for the server; only Next's cache is writable.
COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static
RUN mkdir -p .next/cache && chown node:node .next/cache
USER node
EXPOSE 3000
# Next's server exits on SIGTERM (requests in progress are not drained).
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=10s --timeout=5s --start-period=20s --retries=3 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:'+(process.env.PORT||3000)+'/').then(r=>process.exit(r.status<500?0:1),()=>process.exit(1))"]
CMD ["node", "server.js"]
