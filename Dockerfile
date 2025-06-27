# Build stage
FROM golang:1.22-alpine AS builder

# Set working directory
WORKDIR /app

# Copy dependency files first for better caching
COPY go.mod .
RUN go mod download

# Copy project files
COPY . .

# Build application
RUN CGO_ENABLED=0 GOOS=linux go build -o financial-bot

# Final stage
FROM alpine:latest

# Install required dependencies
RUN apk --no-cache add ca-certificates

# Create app directory
RUN mkdir -p /app/data

# Copy binary from builder
COPY --from=builder /app/financial-bot /app/

# Set working directory
WORKDIR /app

# Expose port (required for Render)
EXPOSE 8080

# Run the application
CMD ["./financial-bot"]