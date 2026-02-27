# Build stage
FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o schemify ./cmd/schemify

# Runtime stage
FROM alpine:3.19
WORKDIR /app
COPY --from=builder /build/schemify .
ENTRYPOINT ["/app/schemify"]
