# Multi-stage build for Lyon service
FROM golang:1.26-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o lyon .

# Final runtime image
FROM alpine:3.19

# Install ca-certificates for HTTPS and wget for healthcheck
RUN apk --no-cache add ca-certificates wget

WORKDIR /root/

# Copy binary from builder
COPY --from=builder /app/lyon .

# Copy config, templates, static files, and prompts
COPY --from=builder /app/config.yaml .
COPY --from=builder /app/dashboard/templates ./dashboard/templates
COPY --from=builder /app/dashboard/static ./dashboard/static
COPY --from=builder /app/prompts ./prompts

# Expose ports
EXPOSE 8081 8082

# Run the application
CMD ["./lyon"]
