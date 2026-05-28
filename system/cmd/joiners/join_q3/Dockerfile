FROM golang:1.24 AS builder
WORKDIR /app
COPY . .
RUN go build -o /bin/join_q3 ./cmd/joiners/join_q3

FROM debian:bookworm-slim
COPY --from=builder /bin/join_q3 /bin/join_q3
ENTRYPOINT ["/bin/join_q3"]
