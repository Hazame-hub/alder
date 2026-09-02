# Alder ships as one binary in a distroless image.
#
# The SPA is embedded into the binary with embed.FS, so there is nothing to
# serve from disk, nothing to mount, and no second container.

# --- the single-page application ---------------------------------------------
FROM node:22-bookworm-slim AS web
WORKDIR /web

# The lockfile first, so a source-only change reuses the install layer.
COPY web/package.json web/package-lock.json ./
RUN npm ci

# The TypeScript client is generated from the same spec the Go server is
# generated from, so the spec has to be present for the build.
COPY api/ /api/
COPY web/ ./
RUN npm run build

# --- the binary ---------------------------------------------------------------
FROM golang:1.25-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# The SPA build output lands where //go:embed expects it.
COPY --from=web /internal/web/dist/ ./internal/web/dist/

ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/alder ./cmd/alder

# --- the image ----------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

# Alder verifies directory certificates against the system roots unless a
# connection supplies its own CA, so the image needs them.
COPY --from=build /out/alder /usr/local/bin/alder

USER nonroot:nonroot

# 8443 is the default listen address. Alder refuses to start without TLS unless
# --allow-http says something in front of it terminates TLS, which is the usual
# arrangement in a cluster.
EXPOSE 8443

ENTRYPOINT ["/usr/local/bin/alder"]
CMD ["serve"]
