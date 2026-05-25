FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/scatter_gather ./cmd/aggregators/scatter_gather

FROM alpine:latest
COPY --from=builder /bin/scatter_gather /bin/scatter_gather
ENTRYPOINT ["/bin/scatter_gather"]

