FROM golang:1.26-alpine AS builder

# Release version stamped into the binary. The CI passes the git tag
# (e.g. v0.6.1); local builds default to the dev version.
ARG VERSION=v0.6.1-dev

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
  -ldflags="-s -w -X github.com/sanonone/kektordb/internal/version.Version=${VERSION}" \
  -trimpath -o kektordb ./cmd/kektordb

FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /root/

COPY --from=builder /app/kektordb .

RUN mkdir -p /data /docs

ENV KEKTOR_PORT=:9091
ENV KEKTOR_DATA_DIR=/data
ENV KEKTOR_TOKEN=""

EXPOSE 9091
EXPOSE 9092

VOLUME ["/data"]

CMD ["./kektordb"]
