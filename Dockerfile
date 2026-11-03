# Stage 1: Build the Go binary
FROM golang:alpine AS builder

WORKDIR /app

ENV GOTOOLCHAIN=auto

# Install git and ca-certificates
RUN apk add --no-cache git ca-certificates tzdata

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o smartfactory .

# Stage 2: Minimal runtime image
FROM alpine:latest

WORKDIR /app

# Install certificates and timezone data (Asia/Bangkok)
RUN apk --no-cache add ca-certificates tzdata
ENV TZ=Asia/Bangkok

# Copy binary and templates from builder
COPY --from=builder /app/smartfactory /app/smartfactory
COPY --from=builder /app/templates /app/templates
COPY --from=builder /app/static /app/static
COPY --from=builder /app/config.json /app/config.json

# Expose port (Cloud Run will set $PORT env, default 8080)
ENV PORT=8080
EXPOSE 8080

CMD ["/app/smartfactory"]

