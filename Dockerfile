# Multi-stage build producing one small image that serves both the API and the
# built React app, so a single deployed service is all that is required.

# --- 1. Build the React frontend -------------------------------------------
FROM node:22-alpine AS frontend
WORKDIR /app/frontend

# Copy manifests first so the dependency layer is cached between code changes.
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

COPY frontend/ ./
# Empty base URL means "same origin", which is what a single-service deploy wants.
ENV VITE_API_BASE_URL=""
RUN npm run build

# --- 2. Build the Go backend ------------------------------------------------
FROM golang:1.22-alpine AS backend
WORKDIR /src

# The module has no third-party dependencies, so there is nothing to download.
COPY backend/go.mod ./
COPY backend/ ./

# CGO_ENABLED=0 gives a fully static binary that runs on a bare base image.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/sequencer .

# --- 3. Runtime -------------------------------------------------------------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 sequencer

WORKDIR /app
COPY --from=backend /out/sequencer /app/sequencer
COPY --from=frontend /app/frontend/dist /app/web

# The data directory holds the JSON state file. Mount a volume here to keep
# playlists across redeploys; without one the seed data is recreated on boot.
RUN mkdir -p /data && chown sequencer:sequencer /data
VOLUME ["/data"]

ENV PORT=8080 \
    DATA_FILE=/data/state.json \
    STATIC_DIR=/app/web \
    CYCLE_DURATION=5h \
    SYNC_DURATION=15s

USER sequencer
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
    CMD wget -qO- http://127.0.0.1:8080/api/health || exit 1

ENTRYPOINT ["/app/sequencer"]
