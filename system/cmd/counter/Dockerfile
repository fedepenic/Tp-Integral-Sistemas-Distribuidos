FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/counter ./cmd/counter

FROM alpine:latest
COPY --from=builder /bin/counter /bin/counter
ENTRYPOINT ["/bin/counter"]
