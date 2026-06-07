FROM golang:1.21-alpine AS builder
ARG SERVICE_PATH
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /service ./cmd/${SERVICE_PATH}

FROM alpine:3.19
COPY --from=builder /service /service
ENTRYPOINT ["/service"]
