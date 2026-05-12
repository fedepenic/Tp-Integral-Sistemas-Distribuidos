FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/client ./cmd/client

FROM alpine:latest
COPY --from=builder /bin/client /bin/client
ENTRYPOINT ["/bin/client"]
