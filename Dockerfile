# syntax=docker/dockerfile:1

FROM golang:1.26-alpine3.24 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 go build -trimpath -o /out/document-cache-api ./cmd/app

FROM alpine:3.24.1

WORKDIR /app

RUN addgroup -g 23456 appgroup && \
    adduser -D -h /app -G appgroup -u 12345 appuser && \
    mkdir -p /app/data/files /app/log && \
    chown -R appuser:appgroup /app

COPY --from=builder /out/document-cache-api /usr/local/bin/document-cache-api

USER appuser

EXPOSE 8080

CMD ["document-cache-api"]