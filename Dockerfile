# --- Stage 1: Build binary ---
FROM golang:1.26-alpine AS builder

WORKDIR /build

# Install git/certs in case dependencies require them
RUN apk add --no-cache git ca-certificates

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o main .

# --- Stage 2: Create runtime container ---
FROM alpine:3.19

RUN apk add --no-cache ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/main /app/main

# Copy template and static directories (needed by the template engine)
COPY --from=builder /build/templates /app/templates
COPY --from=builder /build/static /app/static

EXPOSE 8080

ENTRYPOINT ["/app/main"]
