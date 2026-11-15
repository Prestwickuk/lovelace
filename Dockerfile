# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/tirbach-server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/tirbach-migrate ./cmd/migrate && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/tirbach-seed ./cmd/seed

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S tirbach && \
    adduser -S -G tirbach -H -D tirbach && \
    mkdir -p /var/lib/lovelace/attachments && \
    chown -R tirbach:tirbach /var/lib/lovelace

COPY --from=builder /out/tirbach-server /usr/local/bin/tirbach-server
COPY --from=builder /out/tirbach-migrate /usr/local/bin/tirbach-migrate
COPY --from=builder /out/tirbach-seed /usr/local/bin/tirbach-seed

USER tirbach

ENV TIRBACH_LISTEN_ADDR=0.0.0.0:8080
EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 \
  CMD ["/usr/local/bin/tirbach-server", "-healthcheck"]

ENTRYPOINT ["/usr/local/bin/tirbach-server"]
