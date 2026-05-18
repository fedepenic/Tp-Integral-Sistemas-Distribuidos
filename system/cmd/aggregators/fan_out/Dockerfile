FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/fan_out ./cmd/aggregators/fan_out

FROM alpine:latest
COPY --from=builder /bin/fan_out /bin/fan_out
ENTRYPOINT ["/bin/fan_out"]

