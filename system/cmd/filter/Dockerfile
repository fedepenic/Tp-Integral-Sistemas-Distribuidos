FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/filter ./cmd/filter

FROM alpine:latest
COPY --from=builder /bin/filter /bin/filter
ENTRYPOINT ["/bin/filter"]
