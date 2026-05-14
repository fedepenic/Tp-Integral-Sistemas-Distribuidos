FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/gateway ./cmd/gateway

FROM alpine:latest
COPY --from=builder /bin/gateway /bin/gateway
ENTRYPOINT ["/bin/gateway"]
