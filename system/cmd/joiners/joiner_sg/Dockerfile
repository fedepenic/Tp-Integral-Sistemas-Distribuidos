FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/joiner_sg ./cmd/joiners/joiner_sg

FROM alpine:latest
COPY --from=builder /bin/joiner_sg /bin/joiner_sg
ENTRYPOINT ["/bin/joiner_sg"]
