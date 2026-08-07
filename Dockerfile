# Stage 1: Build the Go binary.
# Uses the exact Go version from go.mod for reproducible builds.
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# Cache module downloads as a separate layer.
# go.sum changes less often than source code, so this layer is reused.
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a statically-linked binary.
# -ldflags="-s -w" strips debug info (~30% smaller binary).
# CGO_ENABLED=0 disables CGo for a pure Go static binary that runs on
# Alpine (musl) or scratch without glibc.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /app/server ./cmd/server

# Stage 2: Minimal runtime image.
FROM alpine:3.22

# ca-certificates: required for TLS connections to PostgreSQL, Redis,
#   and Kafka (Cloud-managed services use TLS).
# tzdata: timezone support (log timestamps, message created_at).
RUN apk add --no-cache ca-certificates tzdata

# Create a non-root user. Even if the app is compromised, the attacker
# doesn't get root inside the container.
RUN adduser -D -H -h /app appuser

WORKDIR /app
COPY --from=builder /app/server .

# Use the non-root user.
USER appuser

EXPOSE 8080

# Run with CMD (not ENTRYPOINT) so docker run can override the command.
CMD ["./server"]
