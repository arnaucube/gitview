FROM golang:1.27-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /gitview ./cmd
FROM alpine:3.23
RUN apk add --no-cache git ca-certificates
WORKDIR /app
COPY --from=builder /gitview /usr/local/bin/gitview
COPY templates ./templates
COPY static ./static
COPY config.yaml ./config.yaml
RUN mkdir repos
EXPOSE 5555
CMD ["gitview", "--config", "/app/config.yaml"]
