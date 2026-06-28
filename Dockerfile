# Builder stage
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache ca-certificates

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./main.go
RUN CGO_ENABLED=0 GOOS=linux go build -o /conduit ./...

# Runtime stage
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /conduit /conduit

ENV CONFIG_FILE="/etc/conduit/config.json"

ENTRYPOINT ["/conduit"]
