# Dockerfile for Application Simulator
FROM golang:1.23-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build app binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /app-bin ./simulator/app/app.go

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy binary from builder
COPY --from=builder /app-bin ./app

# Run app
ENTRYPOINT ["./app"]
