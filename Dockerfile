# Alder ships as one binary in a distroless image.
#
# The SPA is embedded into the binary with embed.FS from M2 onward, so there is
# nothing to serve from disk and nothing to mount.

FROM golang:1.25-bookworm AS build
WORKDIR /src

# Dependencies first, so that a source-only change reuses the module layer.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/alder ./cmd/alder

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/alder /usr/local/bin/alder
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/alder"]
