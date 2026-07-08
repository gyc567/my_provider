# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o /service \
    ./cmd/main.go

# Runtime stage
FROM gcr.io/distroless/base:latest-amd64

COPY --from=builder /service /service

WORKDIR /app

ENTRYPOINT ["/service"]
