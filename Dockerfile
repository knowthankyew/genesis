FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod ./
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /genesis ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=builder /genesis /genesis
USER nonroot:nonroot

EXPOSE 9000/tcp 9001/udp 8080/tcp

ENTRYPOINT ["/genesis"]
