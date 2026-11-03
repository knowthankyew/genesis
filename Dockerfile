FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod ./
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /genesis-server ./cmd/server

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /genesis-server /usr/local/bin/genesis-server

EXPOSE 9000/tcp 9001/udp 8080/tcp

ENTRYPOINT ["genesis-server"]
