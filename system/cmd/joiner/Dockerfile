FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/joiner ./cmd/joiner

FROM alpine:latest
COPY --from=builder /bin/joiner /bin/joiner
ENTRYPOINT ["/bin/joiner"]
