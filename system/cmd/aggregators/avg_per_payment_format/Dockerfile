FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/avg_per_payment_format ./cmd/aggregators/avg_per_payment_format

FROM alpine:latest
COPY --from=builder /bin/avg_per_payment_format /bin/avg_per_payment_format
ENTRYPOINT ["/bin/avg_per_payment_format"]

