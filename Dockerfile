FROM golang:1.26-alpine AS build

WORKDIR /src

# Cache dependency downloads
COPY go.mod go.sum ./
RUN go mod download

# Build a statically-linked binary
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o /goober-bot .

# ---
FROM scratch

# CA certs for HTTPS (NOAA & Telegram APIs)
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=build /goober-bot /goober-bot

# Persistent database storage
VOLUME /data

ENTRYPOINT ["/goober-bot"]
