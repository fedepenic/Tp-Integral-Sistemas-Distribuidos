FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /bin/join_q2 ./cmd/joiners/join_q2
FROM alpine:latest
COPY --from=builder /bin/join_q2 /bin/join_q2
ENTRYPOINT ["/bin/join_q2"]