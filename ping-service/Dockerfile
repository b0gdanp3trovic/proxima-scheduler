FROM golang:1.20 AS builder

WORKDIR /app

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ping-service main.go

FROM alpine:latest

RUN apk add --no-cache ca-certificates

COPY --from=builder /app/ping-service /

EXPOSE 8080

CMD ["/ping-service"]