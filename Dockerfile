# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

# Build metadata injected at build time.
ARG BUILD_VERSION=dev
ARG BUILD_COMMIT=unknown
ARG BUILD_TIME=unknown

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s \
        -X main.BuildVersion=${BUILD_VERSION} \
        -X main.BuildCommit=${BUILD_COMMIT} \
        -X main.BuildTime=${BUILD_TIME}" \
    -o /service \
    ./cmd/main.go

# Runtime stage
FROM gcr.io/distroless/base:latest-amd64

COPY --from=builder /service /service

WORKDIR /app

ENTRYPOINT ["/service"]
