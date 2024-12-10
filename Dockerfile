# Build stage
FROM golang:1.23.3-alpine AS builder

# Install build dependencies
RUN apk add --no-cache gcc musl-dev

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o main .

# Final stage
FROM alpine:latest

# Install CA certificates for HTTPS connections
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from builder
COPY --from=builder /app/main .
# Copy config file
COPY --from=builder /app/config.yaml .

# Expose the application port
EXPOSE 8080

# Environment variables with defaults
ENV COUCHBASE_URL=""
ENV COUCHBASE_BUCKET="fndds"
ENV COUCHBASE_USER=""
ENV COUCHBASE_PWD=""
ENV PORT="8080"

# Run the binary
CMD ["./main"]
