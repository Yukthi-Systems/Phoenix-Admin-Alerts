# Builder Stage
FROM golang:1.26-alpine AS builder

# Install System Dependencies
RUN apk add --no-cache git ca-certificates

# Set working directory
WORKDIR /app

# Copy dependency manifests and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /app/phoenix-admin-alerts \
    ./cmd/app/

# Final Stage
FROM alpine:latest

# Install System Dependencies
RUN apk add --no-cache ca-certificates tzdata

# Set working directory
WORKDIR /app

# Copy the compiled binary from the builder stage
COPY --from=builder /app/phoenix-admin-alerts /app/phoenix-admin-alerts


# Run the binary
ENTRYPOINT ["/app/phoenix-admin-alerts"]

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD pgrep phoenix-admin-alerts > /dev/null || exit 1