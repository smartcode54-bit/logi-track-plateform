# syntax=docker/dockerfile:1
# Next.js standalone server for the compose `web` service (developer-spec.md §10.12, §15;
# Appendix E §E.9.4; issue TW2). Build context: the repository root, so the pnpm workspace
# (lockfile, shared-docs) resolves:
#   docker build -f logitrack-web/Dockerfile .
# The image holds only public NEXT_PUBLIC_* values, inlined at build time. Server-only values
# (GO_API_INTERNAL_URL, SESSION_COOKIE_*, WEB_PUBLIC_ORIGIN, JWT_*, ...) arrive at runtime from
# compose and are never build arguments (§16.5). logitrack-web/Dockerfile.dockerignore keeps
# every .env* file out of the context.
ARG NODE_IMAGE=node:22.23.3-alpine@sha256:0a7108bf6c7bf5de370ffb1a3ed6be93d405b43ff159f681a8d18c0e2bc2e402

FROM ${NODE_IMAGE} AS deps
ENV PNPM_HOME=/pnpm \
    PATH=/pnpm:$PATH \
    COREPACK_ENABLE_DOWNLOAD_PROMPT=0 \
    HUSKY=0 \
    CI=1
WORKDIR /repo
# Every workspace member's manifest, so the frozen lockfile validates (Appendix E §E.9.4).
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY logitrack-web/package.json logitrack-web/
COPY logitrack-web/functions/package.json logitrack-web/functions/
COPY shared-docs/package.json shared-docs/
# pnpm comes from the root package.json "packageManager" field through corepack.
RUN corepack enable pnpm
RUN --mount=type=cache,id=logitrack-pnpm-store,target=/pnpm/store \
    pnpm install --frozen-lockfile --filter "logi-track..."

FROM deps AS build
# Public build-time values only (§16.1 web-public). The Firebase and App Check values stay until
# TW7 removes the Firebase SDK (P6); firebase/client.ts fails the build when the required ones are
# missing.
ARG NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID=""
ARG NEXT_PUBLIC_FIREBASE_API_KEY=""
ARG NEXT_PUBLIC_FIREBASE_AUTH_DOMAIN=""
ARG NEXT_PUBLIC_FIREBASE_PROJECT_ID=""
ARG NEXT_PUBLIC_FIREBASE_STORAGE_BUCKET=""
ARG NEXT_PUBLIC_FIREBASE_MESSAGING_SENDER_ID=""
ARG NEXT_PUBLIC_FIREBASE_APP_ID=""
ARG NEXT_PUBLIC_FIREBASE_MEASUREMENT_ID=""
ARG NEXT_PUBLIC_APP_CHECK_RECAPTCHA_SITE_KEY=""
ARG NEXT_PUBLIC_APP_CHECK_USE_ENTERPRISE=""
ARG NEXT_PUBLIC_APP_CHECK_DEBUG_TOKEN=""
ENV NEXT_TELEMETRY_DISABLED=1
COPY shared-docs shared-docs
COPY logitrack-web logitrack-web
RUN pnpm --filter logi-track build \
 && pnpm --filter logi-track check:standalone

FROM ${NODE_IMAGE} AS runtime
ENV NODE_ENV=production \
    PORT=3000 \
    HOSTNAME=0.0.0.0 \
    NEXT_TELEMETRY_DISABLED=1
WORKDIR /app
# outputFileTracingRoot is the repository root, so the server sits at logitrack-web/server.js;
# static assets and public/ are not part of the trace and are copied next to it.
COPY --from=build --chown=node:node /repo/logitrack-web/.next/standalone ./
COPY --from=build --chown=node:node /repo/logitrack-web/.next/static ./logitrack-web/.next/static
COPY --from=build --chown=node:node /repo/logitrack-web/public ./logitrack-web/public
USER node
EXPOSE 3000
CMD ["node", "logitrack-web/server.js"]
