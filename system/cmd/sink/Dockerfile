FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/sink ./cmd/sink

FROM alpine:latest
COPY --from=builder /bin/sink /bin/sink
ENTRYPOINT ["/bin/sink"]
