# syntax=docker/dockerfile:1
# One image, every binary (developer-spec.md §2.1): /app/bin/{api,worker,scheduler,migrate,seed,etl,release}.
FROM golang:1.27.2-alpine3.23 AS build
ENV GOTOOLCHAIN=local CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath \
      -ldflags "-s -w -X github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app.Version=${VERSION}" \
      -o /out/ ./cmd/...

FROM alpine:3.23
# ca-certificates for outbound TLS, tzdata for Asia/Bangkok cron; busybox wget serves the healthchecks.
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 10001 logitrack
COPY --from=build /out/ /app/bin/
USER 10001
CMD ["/app/bin/api"]
