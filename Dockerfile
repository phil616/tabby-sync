# syntax=docker/dockerfile:1.7

FROM golang:1.26.4-bookworm AS build

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /out/tabby-config-sync \
    ./cmd/tabby-config-sync \
    && install -d -m 0700 /out/data

FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="tabby-config-sync" \
      org.opencontainers.image.description="Minimal Tabby terminal configuration sync host" \
      org.opencontainers.image.licenses="UNLICENSED"

COPY --from=build /out/tabby-config-sync /usr/local/bin/tabby-config-sync
COPY --from=build --chown=65532:65532 /out/data /data

USER 65532:65532
WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080

ENV TCS_LISTEN_ADDRESS=:8080 \
    TCS_DATABASE_PATH=/data/tabby-sync.db

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/tabby-config-sync", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/tabby-config-sync"]
CMD ["serve"]
