# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/noteG-server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/lovelace-migrate ./cmd/migrate && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/lovelace-seed ./cmd/seed

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S -g 101 lovelace && \
    adduser -S -u 100 -G lovelace -H -D lovelace && \
    mkdir -p /var/lib/lovelace/attachments && \
    chown -R lovelace:lovelace /var/lib/lovelace

COPY --from=builder /out/noteG-server /usr/local/bin/noteG-server
COPY --from=builder /out/lovelace-migrate /usr/local/bin/lovelace-migrate
COPY --from=builder /out/lovelace-seed /usr/local/bin/lovelace-seed

USER lovelace

ENV LOVELACE_LISTEN_ADDR=0.0.0.0:8080
EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 \
  CMD ["/usr/local/bin/noteG-server", "-healthcheck"]

ENTRYPOINT ["/usr/local/bin/noteG-server"]
