FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/fan_in ./cmd/aggregators/fan_in

FROM alpine:latest
COPY --from=builder /bin/fan_in /bin/fan_in
ENTRYPOINT ["/bin/fan_in"]

