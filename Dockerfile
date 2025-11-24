# Build stage
FROM golang:1.25.1-alpine AS builder
WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /news-backend

# Final stage
FROM alpine:3.18
WORKDIR /app

# Copy the binary from builder
COPY --from=builder /news-backend /app/

# Copy data files
COPY data /app/data

# Install runtime dependencies
RUN apk add --no-cache ca-certificates

# Expose the application port
EXPOSE 8080

# Set environment variables
ENV PORT=8080
ENV MONGODB_URI=mongodb://mongodb:27017

# Run the application
CMD ["/app/news-backend"]