FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/max_per_bank ./cmd/aggregators/max_per_bank

FROM alpine:latest
COPY --from=builder /bin/max_per_bank /bin/max_per_bank
ENTRYPOINT ["/bin/max_per_bank"]

