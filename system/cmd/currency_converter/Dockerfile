FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/currency_converter ./cmd/currency_converter

FROM alpine:latest
COPY --from=builder /bin/currency_converter /bin/currency_converter
ENTRYPOINT ["/bin/currency_converter"]
