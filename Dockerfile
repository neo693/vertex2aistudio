# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy dependency configuration
COPY go.mod ./
RUN go mod download

# Copy source code
COPY . .

# Build the Go application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o proxy-server .

# Production stage
FROM alpine:latest

# Install CA certificates to enable secure HTTPS requests
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy pre-built binary
COPY --from=builder /app/proxy-server .

# Port that service listens on
EXPOSE 8080

# Run the server
CMD ["./proxy-server"]
