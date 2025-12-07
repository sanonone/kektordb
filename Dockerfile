FROM golang:1.25.4-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o kektordb cmd/kektordb/main.go

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /app/kektordb .

RUN mkdir -p /data

ENV KEKTOR_PORT=:9091
ENV KEKTOR_DATA_DIR=/data
ENV KEKTOR_TOKEN="" 

EXPOSE 9091

VOLUME ["/data"]

CMD ["./kektordb"]
