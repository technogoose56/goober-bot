FROM golang:1.26-alpine AS build

WORKDIR /src

# Cache dependency downloads
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Build a statically-linked binary
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o /goober-bot .

# ---
FROM scratch

# CA certs for HTTPS (NOAA & Telegram APIs)
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=build /goober-bot /goober-bot

# Persistent database storage
VOLUME /data

ENTRYPOINT ["/goober-bot"]
